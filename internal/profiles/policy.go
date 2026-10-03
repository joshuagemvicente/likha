package profiles

import (
	"fmt"
	"strings"

	"likha/internal/explore"
	"likha/internal/tools"
)

// ChildCapableTools is the fixed ceiling of tools any agent profile may grant a
// spawned child, in canonical order. ask_user is deliberately excluded: FR-30
// makes it main-only and child contexts have no interaction channel, so
// admitting it here would silently amend an approved product requirement.
// Widening this set requires a v1-spec amendment and a new decisions-table row,
// not an allowlist entry in a profile file.
var ChildCapableTools = []string{"glob", "read", "grep", "task"}

// Resolve builds the effective child scope for a task spawn of profile at the
// given depth under the parent's frozen scope.
//
// The effective set is the profile allowlist — or the full child-capable
// ceiling when the profile declares no tools — narrowed by depth (task exists
// only below explore.MaxDepth, so a depth-2 node of any profile has no task
// tool even if its allowlist names it) and intersected with the parent scope.
// It never widens: an allowlist can only narrow, and a tool the parent lacks
// stays uninvokable. The returned scope carries the profile name as Agent so
// registry availability and refusals attribute to the right identity.
//
// This scope is a dispatch-time input, not the final authority: the effective
// capability is re-intersected at dispatch by the child's tool registry, so a
// profile can never exceed parent or user permissions even if discovery raced
// a permission change between Resolve and dispatch.
//
// Resolve errors only on unknown tool names and on an empty effective set;
// reserved-name and shape validation is the discovery layer's job.
func Resolve(profile Profile, parent tools.Scope, depth int) (tools.Scope, error) {
	known := make(map[string]bool, len(ChildCapableTools))
	for _, name := range ChildCapableTools {
		known[name] = true
	}
	permitted := make(map[string]bool, len(ChildCapableTools))
	if len(profile.Tools) == 0 {
		// An absent tools field grants the full child-capable ceiling; it can
		// never exceed it, because only these names are ever admitted.
		for _, name := range ChildCapableTools {
			permitted[name] = true
		}
	} else {
		seen := make(map[string]bool, len(profile.Tools))
		var unknown []string
		for _, declared := range profile.Tools {
			name := strings.ToLower(declared)
			if !known[name] {
				if !seen[name] {
					unknown = append(unknown, name)
					seen[name] = true
				}
				continue
			}
			permitted[name] = true
		}
		if len(unknown) > 0 {
			return tools.Scope{}, fmt.Errorf("profile %q allows unknown tools: %s", profile.Name, strings.Join(unknown, ", "))
		}
	}
	if depth >= explore.MaxDepth {
		delete(permitted, "task")
	}
	// A nil parent allowlist permits the registered main catalog; a non-nil
	// allowlist can only narrow.
	for name := range permitted {
		if parent.Allowed != nil && !parent.Allowed[name] {
			delete(permitted, name)
		}
	}
	if len(permitted) == 0 {
		return tools.Scope{}, fmt.Errorf("profile %q has no permitted tools under the current permissions", profile.Name)
	}
	return tools.Scope{Mode: parent.Mode, Agent: profile.Name, Allowed: permitted}, nil
}
