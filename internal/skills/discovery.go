package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxSkills is the largest number of valid skills LoadCatalog advertises.
// Extra valid identities are listed in Catalog.OverCap and must never be
// advertised silently; the user reduces the set instead.
const MaxSkills = 32

const maxCatalogErrors = 256

// Skill is one advertised skill identity. Only the exported text fields
// travel as JSON to the model; Fingerprint and Size are runtime-only
// integrity data (see ReadSkill).
type Skill struct {
	// Name equals the directory that holds SKILL.md and the frontmatter
	// name, in the validSkillName form.
	Name string
	// Description is the trimmed frontmatter description (≤1 KiB).
	Description string
	// Origin is the constant "global": discovery only ever loads the
	// private global skills directory, and the concrete path stays out of
	// advertised text.
	Origin string
	// Fingerprint is the hex sha256 of the full SKILL.md bytes at
	// discovery time.
	Fingerprint string `json:"-"`
	// Size is the full SKILL.md size in bytes.
	Size int `json:"-"`
}

// Catalog is the result of one discovery pass over <stateDir>/skills.
type Catalog struct {
	// Skills are the at most MaxSkills valid identities, sorted by name;
	// these are the entries allowed to be advertised.
	Skills []Skill
	// OverCap holds valid identities beyond the cap, sorted by name. They
	// must NOT be advertised for invocation; /skills surfaces their
	// identity list so the user can trim the set.
	OverCap []Skill
	// Errors is a visible, bounded list of named rejections: malformed
	// frontmatter, invalid names, symlinks, missing or undrivable files,
	// oversized bodies, and the over-cap notice when present. Each
	// rejected identity is absent from Skills — it is never silently
	// dropped or silently included.
	Errors []string
}

// LoadCatalog discovers every skill directory under <stateDir>/skills and
// returns the identities that may be advertised plus a visible error for
// each rejected one. It never fails wholesale: a missing state or
// skills directory is a normal empty state and yields an empty catalog with
// zero errors, while every individual bad skill becomes an Errors entry and
// is skipped.
//
// Discovery loads only <stateDir>/skills/<name>/SKILL.md. There are no
// repository roots, remote catalogs, recursive names, scripts, resource
// includes, or executables, and nothing under the directory other than that
// one file is read. Symlinks are rejected by name, as are paths that
// resolve outside the state directory.
func LoadCatalog(stateDir string) Catalog {
	var cat Catalog
	skillsDir, present, err := resolveSkillsDir(stateDir)
	if err != nil {
		cat.Errors = []string{clamp(fmt.Sprintf("skill catalog: %v", err))}
		return cat
	}
	if !present {
		return cat
	}
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		cat.Errors = []string{clamp(fmt.Sprintf("skill catalog: skills directory is unreadable: %v", err))}
		return cat
	}

	var valid []Skill
	var errs []string
	for _, entry := range entries {
		dirName := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			errs = append(errs, clamp(fmt.Sprintf("skill %q: directory is a symlink; skill directories must be real directories", clamp(dirName))))
			continue
		}
		if !entry.IsDir() {
			// Regular files and other non-directory entries are not skill
			// identities at all; they are ignored so that stray notes or
			// editor droppings do not spam the catalog.
			continue
		}
		if !validSkillName(dirName) {
			errs = append(errs, clamp(fmt.Sprintf("skill %q: invalid name; directory names must be 1-%d lowercase a-z characters, digits, or interior hyphens",
				clamp(dirName), maxSkillNameBytes)))
			continue
		}
		skill, err := loadSkill(skillsDir, dirName)
		if err != nil {
			errs = append(errs, clamp(fmt.Sprintf("skill %q: %v", clamp(dirName), err)))
			continue
		}
		valid = append(valid, skill)
	}

	sort.Slice(valid, func(i, j int) bool { return valid[i].Name < valid[j].Name })
	if len(valid) > MaxSkills {
		cat.OverCap = append(cat.OverCap, valid[MaxSkills:]...)
		valid = valid[:MaxSkills]
		errs = append([]string{fmt.Sprintf("skill catalog exceeds %d entries; reduce the set before it is advertised", MaxSkills)}, errs...)
	}
	if len(errs) > maxCatalogErrors {
		suppressed := len(errs) - (maxCatalogErrors - 1)
		errs = append(errs[:maxCatalogErrors-1], fmt.Sprintf("skill catalog: %d more errors suppressed", suppressed))
	}
	cat.Skills = valid
	cat.Errors = errs
	return cat
}

// ReadSkill re-reads and re-validates a skill previously returned by
// LoadCatalog and returns its Markdown body for one invocation.
//
// The file is rechecked exactly as at discovery time: same path containment,
// symlink rejection, size bound, and frontmatter parse. The recomputed
// name, description, and fingerprint must all still equal the expected
// identity; any drift returns a "changed on disk" error and asks for a
// catalog refresh instead of loading an unexpected replacement. Historical
// bodies stay whatever the caller already persisted; nothing is reloaded
// from here in the background.
func ReadSkill(stateDir string, expected Skill) (body string, err error) {
	if !validSkillName(expected.Name) {
		return "", errors.New(clamp(fmt.Sprintf("skill %q is not a valid skill name; refresh the catalog", clamp(expected.Name))))
	}
	skillsDir, present, err := resolveSkillsDir(stateDir)
	if err != nil {
		return "", errors.New(clamp(fmt.Sprintf("skill %q: %v", clamp(expected.Name), clamp(err.Error()))))
	}
	if !present {
		return "", errors.New(clamp(fmt.Sprintf("skill %q: the skills directory is missing on disk; refresh the catalog", clamp(expected.Name))))
	}
	data, err := loadSkillFile(skillsDir, expected.Name)
	if err != nil {
		return "", errors.New(clamp(fmt.Sprintf("skill %q: %v", clamp(expected.Name), clamp(err.Error()))))
	}
	name, description, body, err := parseSkillFile(data)
	if err != nil {
		return "", errors.New(clamp(fmt.Sprintf("skill %q: %v", clamp(expected.Name), clamp(err.Error()))))
	}
	if name != expected.Name || description != expected.Description || fingerprintData(data) != expected.Fingerprint {
		return "", fmt.Errorf("skill %q changed on disk since discovery; refresh the catalog", clamp(expected.Name))
	}
	return body, nil
}

// resolveSkillsDir resolves <stateDir>/skills. present is false when the
// state or skills directory does not exist, which is a normal empty state:
// the catalog is empty and carries no errors. A skills directory that is a
// symlink, is not a directory, or resolves outside the state directory is an
// error that the caller surfaces visibly.
func resolveSkillsDir(stateDir string) (skillsDir string, present bool, err error) {
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
	skillsAbs := filepath.Join(stateCanonical, "skills")
	info, err := os.Lstat(skillsAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("skills directory is unreadable: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("skills directory is a symlink; skill roots must be real directories")
	}
	if !info.IsDir() {
		return "", false, errors.New("skills directory is not a directory")
	}
	skillsCanonical, err := filepath.EvalSymlinks(skillsAbs)
	if err != nil {
		return "", false, fmt.Errorf("skills directory does not resolve: %v", err)
	}
	if skillsCanonical != skillsAbs {
		// canonical = <canonical state>/skills unless something replaced the
		// entry with a symlink or escape between checks.
		return "", false, errors.New("skills directory resolves outside the state directory")
	}
	return skillsAbs, true, nil
}

// loadSkillFile reads the full SKILL.md of one skill after re-checking the
// directory identity, path containment, and file kind that discovery
// requires. Shared by LoadCatalog and ReadSkill so both enforce identical
// file-level rules.
func loadSkillFile(skillsDir, name string) ([]byte, error) {
	dir, err := loadSkillDir(skillsDir, name)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "SKILL.md")
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("SKILL.md is missing")
		}
		return nil, fmt.Errorf("SKILL.md is unreadable: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("SKILL.md is a symlink; skill files must be real files")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("SKILL.md is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("SKILL.md is missing")
		}
		return nil, fmt.Errorf("SKILL.md is unreadable: %v", err)
	}
	defer file.Close()
	// Re-stat the open handle so a swap between Lstat and Open is caught
	// when it mattered; a symlinked opening still leaks nothing because the
	// size-bound read follows it only if it escaped both checks.
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("SKILL.md is unreadable: %v", err)
	}
	if !opened.Mode().IsRegular() {
		return nil, errors.New("SKILL.md is not a regular file")
	}
	if opened.Size() > maxSkillFileBytes {
		return nil, fmt.Errorf("SKILL.md is %d bytes; the limit is %d bytes (32 KiB body plus frontmatter)",
			opened.Size(), maxSkillFileBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSkillFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("SKILL.md is unreadable: %v", err)
	}
	if len(data) > maxSkillFileBytes {
		return nil, fmt.Errorf("SKILL.md is larger than %d bytes; the limit is set by the 32 KiB body cap plus frontmatter", maxSkillFileBytes)
	}
	return data, nil
}

// loadSkillDir checks that <skillsDir>/<name> is a real directory that
// resolves inside skillsDir and returns its filesystem path.
func loadSkillDir(skillsDir, name string) (string, error) {
	dir := filepath.Join(skillsDir, name)
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("skill directory is missing")
		}
		return "", fmt.Errorf("skill directory is unreadable: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("skill directory is a symlink; skill directories must be real directories")
	}
	if !info.IsDir() {
		return "", errors.New("skill directory is not a directory")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("skill directory does not resolve: %v", err)
	}
	if !underDir(resolved, skillsDir) {
		return "", errors.New("skill directory resolves outside the skills directory")
	}
	return dir, nil
}

// loadSkill reads and validates one skill directory for advertisement.
func loadSkill(skillsDir, name string) (Skill, error) {
	data, err := loadSkillFile(skillsDir, name)
	if err != nil {
		return Skill{}, err
	}
	parsed, description, _, err := parseSkillFile(data)
	if err != nil {
		return Skill{}, err
	}
	if parsed != name {
		return Skill{}, fmt.Errorf("frontmatter name %q does not match the directory name", clamp(parsed))
	}
	return Skill{
		Name:        name,
		Description: description,
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
