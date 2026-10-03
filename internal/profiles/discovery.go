package profiles

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// MaxProfiles is the largest number of valid profiles LoadCatalog
// advertises. Extra valid identities are listed in Catalog.OverCap and must
// never be advertised silently; the user reduces the set instead.
const MaxProfiles = 32

const maxCatalogErrors = 256

// Reserved names are built-in or runtime identities that discovery must not
// shadow: explore and review are live built-ins, implement is the named
// future write-capable built-in that a read-only profile would only
// misrepresent, and main and task are runtime words. A directory using one
// is a named catalog error and is never advertised or invocable.
var Reserved = map[string]bool{
	"main":      true,
	"explore":   true,
	"task":      true,
	"review":    true,
	"implement": true,
}

// Profile is one advertised profile identity. Only the exported text fields
// travel as JSON to the model; Fingerprint and Size are runtime-only
// integrity data (see ReadProfile).
type Profile struct {
	// Name equals the directory that holds AGENT.md and the frontmatter
	// name, in the validProfileName form, and is never in Reserved.
	Name string
	// Description is the trimmed frontmatter description (≤1 KiB).
	Description string
	// Model is the exact requested model id from the frontmatter, or empty
	// to inherit the parent run model. Whether the configured provider
	// serves the id is enforced by the caller at dispatch.
	Model string
	// Tools is the normalized allowlist from the frontmatter — entries
	// trimmed, lowercased, deduplicated preserving file order — or nil
	// when the field is absent, which grants the full child-capable
	// ceiling. Membership was checked at discovery; the effective set is
	// still parent capabilities ∩ user/mode policy ∩ this allowlist.
	Tools []string
	// Origin is the constant "global": discovery only ever loads the
	// private global agents directory, and the concrete path stays out of
	// advertised text.
	Origin string
	// Fingerprint is the hex sha256 of the full AGENT.md bytes at
	// discovery time.
	Fingerprint string `json:"-"`
	// Size is the full AGENT.md size in bytes.
	Size int `json:"-"`
}

// Catalog is the result of one discovery pass over <stateDir>/agents.
type Catalog struct {
	// Profiles are the at most MaxProfiles valid identities, sorted by
	// name; these are the entries allowed to be advertised.
	Profiles []Profile
	// OverCap holds valid identities beyond the cap, sorted by name. They
	// must NOT be advertised for invocation; /agents surfaces their
	// identity list so the user can trim the set.
	OverCap []Profile
	// Errors is a visible, bounded list of named rejections: malformed
	// frontmatter, invalid or reserved names, unknown tools, symlinks,
	// missing or undrivable files, oversized bodies, and the over-cap
	// notice when present. Each rejected identity is absent from Profiles
	// — it is never silently dropped or silently included.
	Errors []string
}

// LoadCatalog discovers every profile directory under <stateDir>/agents and
// returns the identities that may be advertised plus a visible error for
// each rejected one. It never fails wholesale: a missing state or agents
// directory is a normal empty state and yields an empty catalog with zero
// errors, while every individual bad profile becomes an Errors entry and is
// skipped.
//
// Discovery loads only <stateDir>/agents/<name>/AGENT.md. There are no
// repository roots, remote catalogs, recursive names, scripts, includes, or
// executables, and nothing under the directory other than that one file is
// read. Symlinks are rejected by name, as are paths that resolve outside
// the state directory.
func LoadCatalog(stateDir string) Catalog {
	var cat Catalog
	agentsDir, present, err := resolveAgentsDir(stateDir)
	if err != nil {
		cat.Errors = []string{clamp(fmt.Sprintf("profile catalog: %v", err))}
		return cat
	}
	if !present {
		return cat
	}
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		cat.Errors = []string{clamp(fmt.Sprintf("profile catalog: agents directory is unreadable: %v", err))}
		return cat
	}

	var valid []Profile
	var errs []string
	for _, entry := range entries {
		dirName := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			errs = append(errs, clamp(fmt.Sprintf("profile %q: directory is a symlink; profile directories must be real directories", clamp(dirName))))
			continue
		}
		if !entry.IsDir() {
			// Regular files and other non-directory entries are not
			// profile identities at all; they are ignored so that stray
			// notes or editor droppings do not spam the catalog.
			continue
		}
		if !validProfileName(dirName) {
			errs = append(errs, clamp(fmt.Sprintf("profile %q: invalid name; directory names must be 1-%d lowercase a-z characters, digits, or interior hyphens",
				clamp(dirName), maxProfileNameBytes)))
			continue
		}
		if Reserved[dirName] {
			errs = append(errs, clamp(fmt.Sprintf("profile %q: reserved name; main, explore, task, review, and implement cannot be defined by files", clamp(dirName))))
			continue
		}
		profile, err := loadProfile(agentsDir, dirName)
		if err != nil {
			errs = append(errs, clamp(fmt.Sprintf("profile %q: %v", clamp(dirName), err)))
			continue
		}
		valid = append(valid, profile)
	}

	sort.Slice(valid, func(i, j int) bool { return valid[i].Name < valid[j].Name })
	if len(valid) > MaxProfiles {
		cat.OverCap = append(cat.OverCap, valid[MaxProfiles:]...)
		valid = valid[:MaxProfiles]
		errs = append([]string{fmt.Sprintf("profile catalog exceeds %d entries; reduce the set before it is advertised", MaxProfiles)}, errs...)
	}
	if len(errs) > maxCatalogErrors {
		suppressed := len(errs) - (maxCatalogErrors - 1)
		errs = append(errs[:maxCatalogErrors-1], fmt.Sprintf("profile catalog: %d more errors suppressed", suppressed))
	}
	cat.Profiles = valid
	cat.Errors = errs
	return cat
}

// ReadProfile re-reads and re-validates a profile previously returned by
// LoadCatalog and returns its Markdown body (the profile instructions) for
// one dispatch.
//
// The file is rechecked exactly as at discovery time: same path
// containment, symlink rejection, size bound, and frontmatter parse. The
// recomputed name, description, model, tools, and fingerprint must all
// still equal the expected identity; any drift returns a "changed on disk"
// error and asks for a catalog refresh instead of loading an unexpected
// replacement. Historical records stay whatever the caller already
// persisted; nothing is reloaded from here in the background.
func ReadProfile(stateDir string, expected Profile) (instructions string, err error) {
	if !validProfileName(expected.Name) {
		return "", errors.New(clamp(fmt.Sprintf("profile %q is not a valid profile name; refresh the catalog", clamp(expected.Name))))
	}
	agentsDir, present, err := resolveAgentsDir(stateDir)
	if err != nil {
		return "", errors.New(clamp(fmt.Sprintf("profile %q: %v", clamp(expected.Name), clamp(err.Error()))))
	}
	if !present {
		return "", errors.New(clamp(fmt.Sprintf("profile %q: the agents directory is missing on disk; refresh the catalog", clamp(expected.Name))))
	}
	data, err := loadProfileFile(agentsDir, expected.Name)
	if err != nil {
		return "", errors.New(clamp(fmt.Sprintf("profile %q: %v", clamp(expected.Name), clamp(err.Error()))))
	}
	name, description, model, tools, body, err := parseProfileFile(data)
	if err != nil {
		return "", errors.New(clamp(fmt.Sprintf("profile %q: %v", clamp(expected.Name), clamp(err.Error()))))
	}
	if name != expected.Name || description != expected.Description || model != expected.Model ||
		!slices.Equal(tools, expected.Tools) || fingerprintData(data) != expected.Fingerprint {
		return "", fmt.Errorf("profile %q: changed on disk since discovery; refresh the catalog", clamp(expected.Name))
	}
	return body, nil
}

// resolveAgentsDir resolves <stateDir>/agents. present is false when the
// state or agents directory does not exist, which is a normal empty state:
// the catalog is empty and carries no errors. An agents directory that is a
// symlink, is not a directory, or resolves outside the state directory is
// an error that the caller surfaces visibly.
func resolveAgentsDir(stateDir string) (agentsDir string, present bool, err error) {
	absolute, err := filepath.Abs(stateDir)
	if err != nil {
		return "", false, fmt.Errorf("state directory path is invalid: %v", err)
	}
	stateCanonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("state directory does not resolve: %v", err)
	}
	agentsAbs := filepath.Join(stateCanonical, "agents")
	info, err := os.Lstat(agentsAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("agents directory is unreadable: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("agents directory is a symlink; agent roots must be real directories")
	}
	if !info.IsDir() {
		return "", false, errors.New("agents directory is not a directory")
	}
	agentsCanonical, err := filepath.EvalSymlinks(agentsAbs)
	if err != nil {
		return "", false, fmt.Errorf("agents directory does not resolve: %v", err)
	}
	if agentsCanonical != agentsAbs {
		// canonical = <canonical state>/agents unless something replaced
		// the entry with a symlink or escape between checks.
		return "", false, errors.New("agents directory resolves outside the state directory")
	}
	return agentsAbs, true, nil
}

// loadProfileFile reads the full AGENT.md of one profile after re-checking
// the directory identity, path containment, and file kind that discovery
// requires. Shared by LoadCatalog and ReadProfile so both enforce identical
// file-level rules.
func loadProfileFile(agentsDir, name string) ([]byte, error) {
	dir, err := loadProfileDir(agentsDir, name)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "AGENT.md")
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("AGENT.md is missing")
		}
		return nil, fmt.Errorf("AGENT.md is unreadable: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("AGENT.md is a symlink; profile files must be real files")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("AGENT.md is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("AGENT.md is missing")
		}
		return nil, fmt.Errorf("AGENT.md is unreadable: %v", err)
	}
	defer file.Close()
	// Re-stat the open handle so a swap between Lstat and Open is caught
	// when it mattered; a symlinked opening still leaks nothing because the
	// size-bound read follows it only if it escaped both checks.
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("AGENT.md is unreadable: %v", err)
	}
	if !opened.Mode().IsRegular() {
		return nil, errors.New("AGENT.md is not a regular file")
	}
	if opened.Size() > maxProfileFileBytes {
		return nil, fmt.Errorf("AGENT.md is %d bytes; the limit is %d bytes (32 KiB body plus frontmatter)",
			opened.Size(), maxProfileFileBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxProfileFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("AGENT.md is unreadable: %v", err)
	}
	if len(data) > maxProfileFileBytes {
		return nil, fmt.Errorf("AGENT.md is larger than %d bytes; the limit is set by the 32 KiB body cap plus frontmatter", maxProfileFileBytes)
	}
	return data, nil
}

// loadProfileDir checks that <agentsDir>/<name> is a real directory that
// resolves inside agentsDir and returns its filesystem path.
func loadProfileDir(agentsDir, name string) (string, error) {
	dir := filepath.Join(agentsDir, name)
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("profile directory is missing")
		}
		return "", fmt.Errorf("profile directory is unreadable: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("profile directory is a symlink; profile directories must be real directories")
	}
	if !info.IsDir() {
		return "", errors.New("profile directory is not a directory")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("profile directory does not resolve: %v", err)
	}
	if !underDir(resolved, agentsDir) {
		return "", errors.New("profile directory resolves outside the agents directory")
	}
	return dir, nil
}

// loadProfile reads and validates one profile directory for advertisement.
func loadProfile(agentsDir, name string) (Profile, error) {
	data, err := loadProfileFile(agentsDir, name)
	if err != nil {
		return Profile{}, err
	}
	parsed, description, model, tools, _, err := parseProfileFile(data)
	if err != nil {
		return Profile{}, err
	}
	if parsed != name {
		return Profile{}, fmt.Errorf("frontmatter name %q does not match the directory name", clamp(parsed))
	}
	return Profile{
		Name:        name,
		Description: description,
		Model:       model,
		Tools:       tools,
		Origin:      "global",
		Fingerprint: fingerprintData(data),
		Size:        len(data),
	}, nil
}

// underDir reports whether child is the parent itself or lies beneath it.
// Both paths are already canonical (resolved by filepath.EvalSymlinks).
func underDir(child, parent string) bool {
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

// fingerprintData hex-encodes the sha256 of the full file bytes.
func fingerprintData(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
