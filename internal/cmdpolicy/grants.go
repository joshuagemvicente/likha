package cmdpolicy

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// GrantScope is what an "Approve always" grant for one command covers
// (specs/approve-always): the exact string, or every single simple command
// whose leading words equal Key's words.
type GrantScope struct {
	Key    string // the exact command, or the prefix words joined by single spaces
	Prefix bool   // false: exact-string grant
	words  []string
}

// exactOnly programs never get a prefix grant: their arguments decide what
// runs (shells, interpreters, wrappers, package executors) or where data
// goes (network tools).
var exactOnly = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"python": true, "python3": true, "node": true, "deno": true, "ruby": true,
	"perl": true, "php": true, "lua": true, "Rscript": true,
	"env": true, "xargs": true, "nohup": true, "timeout": true, "time": true,
	"nice": true, "watch": true, "exec": true, "command": true, "sudo": true, "doas": true,
	"npx": true, "bunx": true, "pnpx": true, "uvx": true, "pipx": true,
	"curl": true, "wget": true, "ssh": true, "scp": true, "sftp": true, "rsync": true,
	"nc": true, "ncat": true, "telnet": true, "ftp": true,
}

// subcommandTools scope a grant to their subcommand: `go test` never covers
// `go build`.
var subcommandTools = map[string]bool{
	"git": true, "go": true, "npm": true, "pnpm": true, "yarn": true, "bun": true,
	"cargo": true, "make": true, "docker": true, "kubectl": true, "gh": true,
	"pip": true, "pip3": true, "uv": true, "poetry": true, "brew": true,
	"dotnet": true, "mvn": true, "gradle": true, "swift": true, "rustup": true,
}

// scriptRunners scope `<tool> run <script>` to the script.
var scriptRunners = map[string]bool{"npm": true, "pnpm": true, "yarn": true, "bun": true}

// exactSubcommands run arbitrary code chosen by their arguments.
var exactSubcommands = map[string]bool{
	"go run": true, "cargo run": true, "go generate": true, "npm exec": true,
	"pnpm exec": true, "pnpm dlx": true, "yarn dlx": true, "bun x": true,
}

var plainToken = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9:_-]*$`)

// ScopeFor decides what an Approve always grant for command covers.
func ScopeFor(command string) GrantScope {
	exact := GrantScope{Key: command}
	words, ok := simpleWords(command)
	if !ok || len(words) == 0 || isAssignment(words[0]) {
		return exact
	}
	program := filepath.Base(words[0])
	if exactOnly[program] {
		return exact
	}
	key := words[:1]
	if subcommandTools[program] {
		if len(words) < 2 || !plainToken.MatchString(words[1]) || exactSubcommands[program+" "+words[1]] {
			return exact
		}
		key = words[:2]
		if scriptRunners[program] && words[1] == "run" {
			if len(words) < 3 || !plainToken.MatchString(words[2]) {
				return exact
			}
			key = words[:3]
		}
	}
	key = append([]string(nil), key...)
	return GrantScope{Key: strings.Join(key, " "), Prefix: true, words: key}
}

// Describe renders the review line's subject.
func (s GrantScope) Describe() string {
	if s.Prefix {
		return "commands starting with " + strconv.Quote(s.Key)
	}
	return "this exact command"
}

// covers reports whether this scope grants command.
func (s GrantScope) covers(command string) bool {
	if !s.Prefix {
		return command == s.Key
	}
	words, ok := simpleWords(command)
	if !ok || len(words) < len(s.words) {
		return false
	}
	for i, word := range s.words {
		if words[i] != word {
			return false
		}
	}
	return true
}

// Grants holds the commands the user approved always for the current
// session. It is safe for concurrent use: the UI owns it, tool goroutines
// read it.
type Grants struct {
	mu     sync.Mutex
	scopes []GrantScope
}

// Allow records ScopeFor(command) for the rest of the session.
func (g *Grants) Allow(command string) {
	if g == nil {
		return
	}
	scope := ScopeFor(command)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, existing := range g.scopes {
		if existing.Prefix == scope.Prefix && existing.Key == scope.Key {
			return
		}
	}
	g.scopes = append(g.scopes, scope)
}

// Allowed reports whether command matches an exact grant, or — only as a
// single simple command — a prefix grant.
func (g *Grants) Allowed(command string) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, scope := range g.scopes {
		if scope.covers(command) {
			return true
		}
	}
	return false
}
