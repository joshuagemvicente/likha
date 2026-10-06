// Package cmdpolicy decides how much user approval one run_command string
// needs (specs/command-permissions). It never executes anything. Danger is
// found by scanning every word of the whole string, quoting ignored, so a
// destructive command cannot hide inside quotes, `$( )`, `sh -c`, or a
// pipeline; the automatic tiers accept only a single simple command. When in
// doubt a command moves to a stricter tier, never a looser one: a false
// positive costs one prompt, a false negative could cost the user's data.
package cmdpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Tier orders approval strictness: lower values are stricter.
type Tier int

const (
	// Refuse never runs; the user can run the command themselves.
	Refuse Tier = iota
	// AlwaysAsk prompts every time and can never be remembered.
	AlwaysAsk
	// Ask prompts; the user may allow the exact command for the session.
	Ask
	// Verify runs without a prompt only in a repository the user trusts.
	Verify
	// ReadOnly runs without a prompt everywhere.
	ReadOnly
)

func (t Tier) String() string {
	switch t {
	case Refuse:
		return "refuse"
	case AlwaysAsk:
		return "always-ask"
	case Ask:
		return "ask"
	case Verify:
		return "verification"
	case ReadOnly:
		return "read-only"
	}
	return "unknown"
}

// Decision is the classification of one command string.
type Decision struct {
	Tier   Tier
	Reason string // human-readable why; empty for an ordinary Ask
	// Check and Fingerprint identify a Verify command for repository trust:
	// the trust record must hold Check with exactly this Fingerprint.
	Check       string
	Fingerprint string
	// Script is the package.json script text a Verify command resolves to,
	// shown to the user; empty for non-script checks.
	Script string
}

// Classify decides the approval tier of command run in root.
func Classify(command, root string) Decision {
	sc := newScope(root)
	if tier, reason := scanDanger(command, sc); tier < Ask {
		return Decision{Tier: tier, Reason: reason}
	}
	words, ok := simpleWords(command)
	if !ok || len(words) == 0 || isAssignment(words[0]) {
		return Decision{Tier: Ask}
	}
	if readOnly(words) {
		return Decision{Tier: ReadOnly, Reason: "read-only inspection"}
	}
	if decision, ok := verification(words, sc); ok {
		return decision
	}
	return Decision{Tier: Ask}
}

// scope carries the repository root spellings that count as "inside".
type scope struct{ roots []string }

func newScope(root string) scope {
	var roots []string
	add := func(path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		for _, existing := range roots {
			if existing == path {
				return
			}
		}
		roots = append(roots, path)
	}
	add(root)
	if abs, err := filepath.Abs(root); err == nil {
		add(abs)
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			add(real)
		}
	}
	return scope{roots: roots}
}

func (sc scope) primary() string {
	if len(sc.roots) == 0 {
		return "."
	}
	return sc.roots[len(sc.roots)-1]
}

func (sc scope) inside(path string) bool {
	for _, root := range sc.roots {
		if path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/") {
			return true
		}
	}
	return false
}

func (sc scope) isRoot(path string) bool {
	for _, root := range sc.roots {
		if filepath.Clean(path) == root {
			return true
		}
	}
	return false
}

// verifyScripts are the package.json scripts that count as verification.
var verifyScripts = map[string]bool{
	"test": true, "test:unit": true, "test:ci": true, "lint": true, "build": true,
	"typecheck": true, "type-check": true, "check": true, "format:check": true,
}

// RepoChecks lists every verification check root currently has with its
// fingerprint. Trusting a repository records this set; a later check runs
// without a prompt only while its fingerprint still matches.
func RepoChecks(root string) map[string]string {
	checks := map[string]string{"go": "", "cargo": "", "python": "", "bun": ""}
	if scripts, err := readScripts(root); err == nil {
		for name := range verifyScripts {
			if _, ok := scripts[name]; ok {
				checks["script:"+name] = scriptFingerprint(scripts, name)
			}
		}
	}
	if fingerprint, ok := makefileFingerprint(root); ok {
		checks["make"] = fingerprint
	}
	return checks
}

// CheckKeys returns the sorted keys of checks, for stable display.
func CheckKeys(checks map[string]string) []string {
	keys := make([]string, 0, len(checks))
	for key := range checks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

const maxManifestBytes = 2 << 20

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestBytes {
		return nil, errors.New("file too large")
	}
	return data, nil
}

func readScripts(root string) (map[string]string, error) {
	data, err := readBounded(filepath.Join(root, "package.json"))
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if manifest.Scripts == nil {
		manifest.Scripts = map[string]string{}
	}
	return manifest.Scripts, nil
}

// scriptFingerprint covers the script and the pre/post hooks npm runs with it.
func scriptFingerprint(scripts map[string]string, name string) string {
	sum := sha256.Sum256([]byte(scripts["pre"+name] + "\x00" + scripts[name] + "\x00" + scripts["post"+name]))
	return hex.EncodeToString(sum[:])
}

// makefileFingerprint hashes the makefile GNU make would read.
func makefileFingerprint(root string) (string, bool) {
	for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
		data, err := readBounded(filepath.Join(root, name))
		if err == nil {
			sum := sha256.Sum256(data)
			return hex.EncodeToString(sum[:]), true
		}
	}
	return "", false
}
