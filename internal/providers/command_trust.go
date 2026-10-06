package providers

import (
	"path/filepath"
	"sync"
)

// commandTrustMu serializes this process's read-modify-write of config.json
// for trust records, which tool goroutines write while the UI runs.
var commandTrustMu sync.Mutex

// CanonicalRepoPath is the key trust records use: absolute with symlinks
// resolved, so equivalent spellings of one repository share a record.
func CanonicalRepoPath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// CommandTrusted reports whether the user trusted root's check with exactly
// this fingerprint. Any read error means untrusted: the command prompts.
func CommandTrusted(stateDir, root, check, fingerprint string) bool {
	if stateDir == "" || check == "" {
		return false
	}
	commandTrustMu.Lock()
	defer commandTrustMu.Unlock()
	cfg, err := LoadStoredConfig(stateDir)
	if err != nil {
		return false
	}
	recorded, ok := cfg.CommandTrust[CanonicalRepoPath(root)].Checks[check]
	return ok && recorded == fingerprint
}

// TrustCommandChecks replaces root's trust record with checks.
func TrustCommandChecks(stateDir, root string, checks map[string]string) error {
	commandTrustMu.Lock()
	defer commandTrustMu.Unlock()
	cfg, err := LoadStoredConfig(stateDir)
	if err != nil {
		return err
	}
	if cfg.CommandTrust == nil {
		cfg.CommandTrust = map[string]StoredCommandTrust{}
	}
	copied := make(map[string]string, len(checks))
	for key, value := range checks {
		copied[key] = value
	}
	cfg.CommandTrust[CanonicalRepoPath(root)] = StoredCommandTrust{Checks: copied}
	return SaveStoredConfig(stateDir, cfg)
}
