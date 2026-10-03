// Package mcp connects Likha to user-configured Model Context Protocol (MCP)
// servers over stdio. Likha launches each server as a child process, performs
// the MCP initialize handshake, exposes the server's tools to the agent loop,
// and calls them on demand. Likha hosts no servers of its own: every server
// comes from the user's mcp.json and its processes are killed on exit.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Tool is a tool exposed by an MCP server. InputSchema is the tool's raw
// JSON schema, passed through to the model untouched.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

const (
	protocolVersion = "2025-03-26"
)

// CallTimeout bounds one tools/call or handshake round-trip. It is a variable
// so tests can shorten it.
var CallTimeout = 60 * time.Second

// Client manages one running MCP server process.
type Client struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	reader  *bufio.Reader
	mu      sync.Mutex
	pending map[int]chan rpcResponse
	nextID  int
	Tools   []Tool
	closed  bool
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Start launches the server process and performs the MCP handshake:
// initialize → notifications/initialized → tools/list.
func Start(name string, cfg ServerConfig, userAgent string) (*Client, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = append(cmd.Environ(), "LIKHA_USER_AGENT="+userAgent)
	for key, value := range cfg.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp %s: stdin pipe: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp %s: stdout pipe: %w", name, err)
	}
	cmd.Stderr = nil // the server's logs must not corrupt the protocol stream
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s: start %q: %w", name, cfg.Command, err)
	}
	c := &Client{
		name:    name,
		cmd:     cmd,
		stdin:   stdin,
		reader:  bufio.NewReaderSize(stdout, 1<<20),
		pending: make(map[int]chan rpcResponse),
	}
	go c.readLoop()
	if err := c.handshake(userAgent); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) handshake(userAgent string) error {
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "likha", "version": strings.TrimPrefix(userAgent, "likha/")},
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"capabilities"`
	}
	if _, err := c.request(ctx, "initialize", params, &result); err != nil {
		return fmt.Errorf("mcp %s: initialize: %w", c.name, err)
	}
	if result.ProtocolVersion != protocolVersion {
		return fmt.Errorf("mcp %s: server speaks protocol %q, Likha speaks %q", c.name, result.ProtocolVersion, protocolVersion)
	}
	if err := c.notify("notifications/initialized", nil); err != nil {
		return fmt.Errorf("mcp %s: initialized notification: %w", c.name, err)
	}
	var list struct {
		Tools []Tool `json:"tools"`
	}
	if _, err := c.request(ctx, "tools/list", nil, &list); err != nil {
		return fmt.Errorf("mcp %s: tools/list: %w", c.name, err)
	}
	c.Tools = list.Tools
	return nil
}

// request sends a JSON-RPC request and waits for its matching response,
// skipping over any notifications and unrelated server requests (v1 servers
// with sampling/root capabilities are unsupported and their requests are
// ignored). The context bounds the wait; tool calls pass a 60 s deadline.
func (c *Client) request(ctx context.Context, method string, params any, result any) (rpcResponse, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return rpcResponse{}, fmt.Errorf("server is stopped")
	}
	c.nextID++
	id := c.nextID
	chanReply := make(chan rpcResponse, 1)
	c.pending[id] = chanReply
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return rpcResponse{}, err
	}
	if _, err := c.stdin.Write(append(payload, '\n')); err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return rpcResponse{}, err
	}
	c.mu.Unlock()

	select {
	case reply := <-chanReply:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		if reply.Error != nil {
			return reply, fmt.Errorf("RPC error %d: %s", reply.Error.Code, reply.Error.Message)
		}
		if result != nil && len(reply.Result) > 0 {
			if err := json.Unmarshal(reply.Result, result); err != nil {
				return reply, fmt.Errorf("decode result: %w", err)
			}
		}
		return reply, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return rpcResponse{}, fmt.Errorf("request timed out: %w", ctx.Err())
	}
}

func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("server is stopped")
	}
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(payload, '\n'))
	return err
}

// Call invokes one tool and returns its text content. isError reports an MCP
// tool-level failure (protocol success, tool error).
func (c *Client) Call(ctx context.Context, tool string, arguments json.RawMessage) (result string, isError bool, err error) {
	params := map[string]any{"name": tool, "arguments": arguments}
	var reply struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if _, err := c.request(ctx, "tools/call", params, &reply); err != nil {
		return "", false, err
	}
	var b strings.Builder
	for _, part := range reply.Content {
		if part.Type == "text" {
			b.WriteString(part.Text)
		}
	}
	return b.String(), reply.IsError, nil
}

// readLoop is the pump started lazily by request; it matches responses to
// pending requests by ID and ignores server-initiated requests/notifications.
func (c *Client) readLoop() {
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			c.failPending(fmt.Errorf("server stopped: %w", err))
			return
		}
		var reply rpcResponse
		if err := json.Unmarshal([]byte(line), &reply); err != nil || reply.ID == 0 {
			continue // notification or unparseable line: ignored in v1
		}
		c.mu.Lock()
		waiter := c.pending[reply.ID]
		c.mu.Unlock()
		if waiter != nil {
			waiter <- reply
		}
	}
}

func (c *Client) failPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, waiter := range c.pending {
		waiter <- rpcResponse{ID: id, Error: &rpcError{Message: err.Error()}}
		delete(c.pending, id)
	}
}

// Close kills the server process and fails any pending calls.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	return c.cmd.Process.Kill()
}
