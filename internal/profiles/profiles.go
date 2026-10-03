// Package profiles implements strict global agent-profile discovery. It
// validates and advertises user-authored identities from
// <stateDir>/agents/<name>/AGENT.md only — a hand-rolled
// name/description/model/tools frontmatter subset, a 1 KiB description, and
// a 32 KiB Markdown body — rejecting symlinks, escapes from the state
// directory, oversized bodies, reserved names, and malformed or duplicate
// identities with visible named errors. A profile is data and prompt, never
// executable code: nothing here registers tools, changes provider
// configuration, approves effects, or executes anything. Instructions load
// on demand through ReadProfile, which refuses files that drifted since
// discovery. See specs/user-agents/spec.md.
package profiles
