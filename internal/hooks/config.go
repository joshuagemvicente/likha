// Package hooks runs the user's own commands at fixed Likha events
// (specs/hooks, FR-45). Hooks come only from the private global
// <stateDir>/hooks.json. They observe, and the blocking events can refuse a
// tool call or a prompt; no hook output can ever approve anything.
package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// FileName is the only hook configuration Likha reads, inside the state
// directory. Repository files never add hooks.
const FileName = "hooks.json"

// Fixed limits (drafting defaults D3 and D6).
const (
	DefaultTimeout  = 10 * time.Second
	MaxTimeout      = 60 * time.Second
	OutputLimit     = 64 << 10
	ReasonLimit     = 1 << 10
	ResultLimit     = 16 << 10
	PayloadVersion  = 1
	maxConfigBytes  = 1 << 20
	errorOnBlock    = "block"
	errorOnAllow    = "allow"
	matchableEvents = "pre_tool and post_tool"
)

// EventName identifies one hook event.
type EventName string

const (
	SessionStart EventName = "session_start"
	PromptSubmit EventName = "prompt_submit"
	PreTool      EventName = "pre_tool"
	PostTool     EventName = "post_tool"
	RunEnd       EventName = "run_end"
	Attention    EventName = "attention"
)

// eventOrder is the fixed listing order for /hooks and validation messages.
var eventOrder = []EventName{SessionStart, PromptSubmit, PreTool, PostTool, RunEnd, Attention}

// Blocking reports whether hooks for the event can refuse what triggered it.
func (e EventName) Blocking() bool { return e == PreTool || e == PromptSubmit }

func knownEvent(name EventName) bool {
	for _, known := range eventOrder {
		if known == name {
			return true
		}
	}
	return false
}

// Hook is one validated hooks.json entry.
type Hook struct {
	Event   EventName
	Index   int
	Name    string
	Command string
	Match   []string
	Timeout time.Duration
	// FailOpen is "on_error": "allow": a failing blocking hook lets the call
	// or prompt through with a warning instead of blocking it.
	FailOpen bool
}

// matches reports whether a pre_tool or post_tool hook applies to the tool's
// canonical name. No match list means every tool.
func (h Hook) matches(tool string) bool {
	if len(h.Match) == 0 {
		return true
	}
	for _, pattern := range h.Match {
		if ok, _ := path.Match(pattern, tool); ok {
			return true
		}
	}
	return false
}

// Config is a loaded, fully validated hooks.json. The zero value has no hooks.
type Config struct {
	Path  string
	Hooks []Hook
}

type fileEntry struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Match   []string `json:"match"`
	Timeout *float64 `json:"timeout"`
	OnError string   `json:"on_error"`
}

type fileConfig struct {
	Hooks map[string][]fileEntry `json:"hooks"`
}

// Path returns the hooks.json location for a state directory.
func Path(stateDir string) string { return filepath.Join(stateDir, FileName) }

// Load reads <stateDir>/hooks.json. A missing file is an empty config and no
// error; Likha never creates the file. Any problem rejects the whole file, so
// a broken configuration never starts some of its hooks.
func Load(stateDir string) (Config, error) {
	file := Path(stateDir)
	cfg := Config{Path: file}
	info, err := os.Stat(file)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", file, err)
	}
	if !info.Mode().IsRegular() {
		return cfg, fmt.Errorf("%s: not a regular file", file)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return cfg, fmt.Errorf("%s: writable by group or others (mode %04o); run chmod go-w on it", file, info.Mode().Perm())
	}
	data, err := readBounded(file)
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", file, err)
	}
	hooks, err := parse(data)
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", file, err)
	}
	cfg.Hooks = hooks
	return cfg, nil
}

func readBounded(file string) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxConfigBytes {
		return nil, fmt.Errorf("larger than %d bytes", maxConfigBytes)
	}
	return data, nil
}

func parse(data []byte) ([]Hook, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var raw fileConfig
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if decoder.More() {
		return nil, errors.New("invalid JSON: trailing data after the top-level object")
	}
	for name := range raw.Hooks {
		if !knownEvent(EventName(name)) {
			return nil, fmt.Errorf("unknown event %q; known events are %s", name, eventList())
		}
	}
	var hooks []Hook
	for _, event := range eventOrder {
		for index, entry := range raw.Hooks[string(event)] {
			hook, err := validateEntry(event, index, entry)
			if err != nil {
				return nil, fmt.Errorf("%s[%d]: %w", event, index, err)
			}
			hooks = append(hooks, hook)
		}
	}
	return hooks, nil
}

func validateEntry(event EventName, index int, entry fileEntry) (Hook, error) {
	hook := Hook{Event: event, Index: index, Name: strings.TrimSpace(entry.Name), Command: strings.TrimSpace(entry.Command), Timeout: DefaultTimeout}
	if hook.Command == "" {
		return Hook{}, errors.New("command is required")
	}
	if hook.Name == "" {
		hook.Name = fmt.Sprintf("%s[%d]", event, index)
	}
	if len(entry.Match) > 0 && event != PreTool && event != PostTool {
		return Hook{}, fmt.Errorf("match is only allowed on %s", matchableEvents)
	}
	for _, pattern := range entry.Match {
		if strings.TrimSpace(pattern) == "" {
			return Hook{}, errors.New("match patterns must be nonempty")
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return Hook{}, fmt.Errorf("match pattern %q: %w", pattern, err)
		}
		hook.Match = append(hook.Match, pattern)
	}
	if entry.Timeout != nil {
		seconds := *entry.Timeout
		if seconds < 1 || seconds > MaxTimeout.Seconds() || seconds != float64(int(seconds)) {
			return Hook{}, fmt.Errorf("timeout must be a whole number of seconds from 1 to %d", int(MaxTimeout.Seconds()))
		}
		hook.Timeout = time.Duration(seconds) * time.Second
	}
	switch entry.OnError {
	case "", errorOnBlock:
	case errorOnAllow:
		hook.FailOpen = true
	default:
		return Hook{}, fmt.Errorf("on_error must be %q or %q", errorOnBlock, errorOnAllow)
	}
	return hook, nil
}

func eventList() string {
	names := make([]string, len(eventOrder))
	for i, event := range eventOrder {
		names[i] = string(event)
	}
	return strings.Join(names, ", ")
}
