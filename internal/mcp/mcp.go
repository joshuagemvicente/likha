// Package mcp connects Likha to user-configured Model Context Protocol (MCP)
// servers over stdio. Likha launches each server as a child process, performs
// the MCP initialize handshake, exposes the server's tools to the agent loop,
// and calls them on demand. Likha hosts no servers of its own: every server
// comes from the user's mcp.json and its processes are killed on exit.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
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

// Client manages one connected MCP server. The transport carries its
// JSON-RPC messages; Client owns request IDs, the handshake and tool calls.
type Client struct {
	name   string
	conn   transport
	mu     sync.Mutex
	nextID int
	Tools  []Tool
	closed bool
}

// transport carries JSON-RPC messages to one server. call sends a request
// and waits for the response carrying the same ID; notify sends a message
// that expects no response; close releases the connection or process.
type transport interface {
	call(ctx context.Context, id int, payload []byte) (rpcResponse, error)
	notify(ctx context.Context, payload []byte) error
	close() error
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

// Start connects to the server and performs the MCP handshake:
// initialize → notifications/initialized → tools/list.
func Start(name string, cfg ServerConfig, userAgent string) (*Client, error) {
	conn, err := startStdio(name, cfg, userAgent)
	if err != nil {
		return nil, err
	}
	c := &Client{name: name, conn: conn}
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
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
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
	c.mu.Unlock()
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return rpcResponse{}, err
	}
	reply, err := c.conn.call(ctx, id, payload)
	if err != nil {
		return reply, err
	}
	if reply.Error != nil {
		return reply, fmt.Errorf("RPC error %d: %s", reply.Error.Code, reply.Error.Message)
	}
	if result != nil && len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return reply, fmt.Errorf("decode result: %w", err)
		}
	}
	return reply, nil
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return fmt.Errorf("server is stopped")
	}
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	return c.conn.notify(ctx, payload)
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

// Close stops the server connection and fails any pending calls.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	return c.conn.close()
}
