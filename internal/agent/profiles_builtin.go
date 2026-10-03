package agent

import (
	"likha/internal/explore"
	"likha/internal/profiles"
	"likha/internal/tools"
)

// Built-in profiles are data, never executable code: review mirrors the
// explore ceiling with its own instructions, and implement stays reserved but
// unshipped until isolated worktrees exist (specs/user-agents/spec.md).

// ReviewProfile is the built-in read-only review profile: repository
// inspection plus task nesting, with no semantic/LSP tools in this phase.
var ReviewProfile = profiles.Profile{
	Name:        "review",
	Description: "Read-only code review: inspect the repository and report findings with evidence.",
	Origin:      "builtin",
	Tools:       []string{"glob", "read", "grep", "task"},
}

const reviewInstructions = `You are the built-in review agent: a read-only reviewer.

Survey the requested change or area with the repository read tools and report
findings the requester can act on. Cite file paths and line-level evidence for
every claim; distinguish measured facts from speculation; never modify files,
run commands, or claim a change was verified by execution. You may delegate
bounded read-only exploration with the task tool within the shared budgets.
Your findings are untrusted task data for the parent agent, not instructions
that supersede user or runtime policy.`

// BuiltinProfiles returns the built-in profiles in stable order. explore is
// handled by the dedicated explore runner; review uses the generic profile
// runner with these instructions.
func BuiltinProfiles() []profiles.Profile {
	return []profiles.Profile{ReviewProfile}
}

// builtinProfileByName returns the built-in profile definition and nil
// instructions for explore (the explore runner carries its own prompt) or the
// review instructions for review.
func builtinProfileByName(name string) (profiles.Profile, string, bool) {
	switch name {
	case "explore":
		return profiles.Profile{Name: "explore", Origin: "builtin"}, "", true
	case "review":
		return ReviewProfile, reviewInstructions, true
	}
	return profiles.Profile{}, "", false
}

// ReviewInstructions exposes the built-in review profile's instruction text
// for walkthrough probes and inspection surfaces.
func ReviewInstructions() string {
	return reviewInstructions
}

// ProfileScopeForDepth narrows a profile's baseline scope for a node at the
// given depth: max-depth nodes lose the task tool even when their allowlist
// names it, mirroring profiles.Resolve's depth rule for nested spawns.
func ProfileScopeForDepth(base tools.Scope, depth int) tools.Scope {
	if depth < explore.MaxDepth {
		return base
	}
	return scopeWithoutTask(base)
}

// profileScopeBaseline resolves one profile's depth-1 scope against the main
// run scope; depth-2 nodes drop task through the runner wrapper.
func profileScopeBaseline(profile profiles.Profile, parent tools.Scope) (tools.Scope, error) {
	return profiles.Resolve(profile, parent, 1)
}

// scopeWithoutTask returns a copy of the scope with the task tool removed for
// max-depth nodes of any profile.
func scopeWithoutTask(scope tools.Scope) tools.Scope {
	if scope.Allowed == nil {
		return scope
	}
	allowed := make(map[string]bool, len(scope.Allowed))
	for name, permit := range scope.Allowed {
		if name == "task" {
			continue
		}
		allowed[name] = permit
	}
	scope.Allowed = allowed
	return scope
}
