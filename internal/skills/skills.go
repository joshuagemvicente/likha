// Package skills implements strict global Markdown skill discovery. It
// validates and advertises identities from <stateDir>/skills/<name>/SKILL.md
// only — a hand-rolled name/description frontmatter subset, a 1 KiB
// description, and a 32 KiB Markdown body — rejecting symlinks, escapes from
// the state directory, oversized bodies, and malformed or duplicate
// identities with visible named errors. Bodies load on demand through
// ReadSkill, which refuses files that drifted since discovery. Nothing here
// registers tools, executes code, or fetches either locally or remotely; see
// specs/markdown-skills/spec.md.
package skills
