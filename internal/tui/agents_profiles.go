package tui

import (
	"fmt"
	"sort"
	"strings"

	"likha/internal/explore"
	"likha/internal/profiles"
)

// profileEffectiveTools mirrors profiles.Resolve with a nil parent scope at a
// depth-permitted node: an absent allowlist grants the full child-capable
// ceiling (profiles.ChildCapableTools) and a present allowlist narrows it.
// Discovery only admits allowlist names inside that set, so no filtering is
// needed here. Dispatch re-intersects the result with the parent's effective
// capabilities and user/mode policy; this is the advertised ceiling, never a
// grant.
func profileEffectiveTools(allowlist []string) []string {
	if len(allowlist) == 0 {
		return append([]string(nil), profiles.ChildCapableTools...)
	}
	return allowlist
}

// profileCatalogLines renders the /agents profile section from the frozen
// catalog the coordinator refreshes at run boundaries and session switches.
// Built-in rows come first, then discovered profiles sorted by name, then
// over-cap identities and named catalog errors. Every profile-derived string
// passes through toolShortText, mirroring how agents_view renders record
// fields, so no raw ANSI or control sequence reaches the terminal. The
// zero-value catalog renders the empty state; nothing here launches a task or
// grants a capability.
func (m *ui) profileCatalogLines() []string {
	var lines []string
	add := func(text string) { lines = append(lines, text) }

	add("Built-in profiles")
	add("explore — built-in · repository read-only: glob, read, grep, plus task where depth permits.")
	add("  Inherits the run's provider/model; the embedded explore prompt stays authoritative.")
	add("review — built-in · repository read/search: glob, read, grep, plus task nesting at depth-permitted nodes.")
	add("  Read-only; no LSP or semantic tools ship in this phase — deliberate degradation, documented here and in the review prompt until a code-intelligence spec extends the ceiling.")
	add("  Inherits the run's provider/model; no discovered file can redefine a built-in.")

	catalog := m.profileCatalog
	if len(catalog.Profiles) == 0 && len(catalog.OverCap) == 0 && len(catalog.Errors) == 0 {
		add("")
		add("No user profiles discovered. Place one profile per directory in the private agents folder as <name>/AGENT.md with name and description frontmatter.")
		return lines
	}

	discovered := append([]profiles.Profile(nil), catalog.Profiles...)
	sort.Slice(discovered, func(i, j int) bool { return discovered[i].Name < discovered[j].Name })
	add("")
	add(fmt.Sprintf("Discovered user profiles · %d advertised", len(discovered)))
	for _, profile := range discovered {
		add("- " + toolShortText(profile.Name, 64) + ": " + profileDescriptionText(profile.Description))
		if model := strings.TrimSpace(profile.Model); model != "" {
			add("  model " + toolShortText(model, 96))
		}
		add("  tools: " + profileToolListText(profileEffectiveTools(profile.Tools)))
		add("  origin " + profileOriginText(profile.Origin))
	}
	if len(catalog.OverCap) > 0 {
		add("")
		add(fmt.Sprintf("Over the %d-identity catalog cap; held out of advertisement and never invocable. Reduce the profile set and refresh the catalog:", profiles.MaxProfiles))
		for _, profile := range catalog.OverCap {
			add("- over cap: " + toolShortText(profile.Name, 64))
		}
	}
	if len(catalog.Errors) > 0 {
		add("")
		add("Named catalog errors (rejected identities are skipped, never hidden):")
		for _, err := range catalog.Errors {
			add("- " + toolShortText(err, 160))
		}
	}
	return lines
}

// profileCeilingLines renders the detail lines for one profile name. For
// explore and review it documents the built-in ceiling; for discovered names
// it renders the frozen catalog entry. The advertised ceiling assumes a nil
// parent scope — every child-capable tool the allowlist admits — and the
// budget text is shared across all profiles. Unknown names stay inspectable
// with a named explanation instead of silently rendering another profile.
func (m *ui) profileCeilingLines(name string) []string {
	var lines []string
	add := func(text string) { lines = append(lines, text) }
	trimmed := strings.TrimSpace(name)
	switch trimmed {
	case "explore":
		add("Built-in explore profile")
		add("Repository-read-only work in a separate child context: glob, read, grep, plus task where depth permits.")
		add("Inherits the run's provider/model; the embedded explore prompt stays authoritative and no discovered file can redefine it.")
	case "review":
		add("Built-in review profile")
		add("Repository read/search work in a separate child context: glob, read, grep, plus task nesting at depth-permitted nodes.")
		add("Inherits the run's provider/model; no discovered file can redefine it.")
		add("Read-only; no LSP or semantic tools ship in this phase. This is deliberate degradation, documented here and in the review prompt until a code-intelligence spec extends the ceiling.")
	default:
		profile, ok := m.lookupDiscoveredProfile(trimmed)
		if !ok {
			add("Profile " + toolShortText(name, 64) + " is not in the frozen run catalog.")
			if m.profileOverCap(trimmed) {
				add("It is over the advertised catalog cap: never advertised, never invocable. Reduce the profile set and refresh the catalog.")
			} else {
				add("Refresh the catalog at the next run boundary; unknown or over-cap names refuse at dispatch and consume no spawn budget.")
			}
			return lines
		}
		add("Profile " + toolShortText(profile.Name, 64))
		add(profileDescriptionText(profile.Description))
		if model := strings.TrimSpace(profile.Model); model != "" {
			add("Model: " + toolShortText(model, 96) + " — refused at dispatch unless the configured provider serves it; no fallback, no substitution.")
		} else {
			add("Model: inherits the run's provider/model.")
		}
		add("Origin: " + profileOriginText(profile.Origin) + " (private global state directory).")
		add("Tool ceiling: " + profileToolListText(profileEffectiveTools(profile.Tools)) + ".")
		add("An allowlist can only narrow. Dispatch re-intersects this ceiling with the parent's effective capabilities and user/mode policy, so a named tool the parent lacks still cannot be invoked.")
		add("task is available only where depth permits; a depth-2 node has no task tool even if its allowlist names it.")
	}
	add("")
	add(fmt.Sprintf("Budgets: depth ≤ %d · %d children execute across the whole tree · %d accepted per main run · %d model requests per child · %s deadline from acceptance including queue and nested waits; the earlier ancestor deadline wins.",
		explore.MaxDepth, explore.MaxExecuting, explore.MaxChildren, explore.MaxRequests, explore.ChildTimeout))
	add("Nested agents reach the configured hosted provider and can multiply cost; time, concurrency, and request limits are not a cost cap. Usage attribution reflects the model actually used, and unknown metrics stay unknown.")
	return lines
}

// agentProfileLabel names the profile behind a task record: explore, review,
// or a discovered profile name. Unknown names return the raw recorded string —
// they are data, and callers safe-render the result like any record field.
// Empty Agent fields belong to Phase 1–3 records, which could only be explore.
func (m *ui) agentProfileLabel(record explore.Record) string {
	name := strings.TrimSpace(record.Agent)
	if name == "" {
		return "explore"
	}
	return name
}

// lookupDiscoveredProfile finds one advertised catalog entry by exact name.
func (m *ui) lookupDiscoveredProfile(name string) (profiles.Profile, bool) {
	for _, profile := range m.profileCatalog.Profiles {
		if profile.Name == name {
			return profile, true
		}
	}
	return profiles.Profile{}, false
}

// profileOverCap reports whether a name is held out of advertisement by the
// catalog cap. Over-cap identities are valid but never advertised or
// invocable, so their detail page explains the hold instead of pretending.
func (m *ui) profileOverCap(name string) bool {
	for _, profile := range m.profileCatalog.OverCap {
		if profile.Name == name {
			return true
		}
	}
	return false
}

// profileDescriptionText bounds a profile description to one safe line.
func profileDescriptionText(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return "no description"
	}
	return toolShortText(description, explore.MaxDescriptionRunes)
}

// profileToolListText renders an effective tool ceiling as a bounded list.
func profileToolListText(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	rendered := make([]string, 0, len(names))
	for _, name := range names {
		rendered = append(rendered, toolShortText(name, 32))
	}
	return strings.Join(rendered, ", ")
}

// profileOriginText reports the catalog origin. Discovery reads only the
// private global state directory, so the only valid origin is global.
func profileOriginText(origin string) string {
	if strings.TrimSpace(origin) == "" {
		return "global"
	}
	return toolShortText(origin, 32)
}
