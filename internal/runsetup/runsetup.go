// Package runsetup assembles the parts of agent.RunOptions that do not
// depend on a user interface: private session persistence, the skill and
// profile catalogs, and repository command trust. The TUI and headless runs
// share it so both drive the agent with the same hooks.
package runsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"likha/internal/actions"
	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/profiles"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/skills"
	"likha/internal/tools"
)

// ApplySession wires a session's private persistence into options: explore
// task records, plan checklist replacements, the multi-file edit journal
// and the /undo change-set history (specs/edit-undo). A nil store or empty session ID leaves the hooks nil, which
// disables the tools that need them.
func ApplySession(options *agent.RunOptions, store *session.Store, sessionID string) {
	if store == nil || sessionID == "" {
		return
	}
	options.SaveTask = store.SaveTask
	// Plan checklist updates persist through the store immediately; the
	// apply hook only writes private session state, never repo or provider
	// state.
	options.PlanApply = func(ctx context.Context, steps []tools.PlanStep) error {
		return store.SavePlan(sessionID, session.Plan{Steps: steps, UpdatedAt: time.Now().UTC()})
	}
	options.EditJournal = func(proposal *actions.EditProposal) error {
		return proposal.SetJournal(EditJournal(store, sessionID))
	}
	options.RecordChangeSet = func(toolCallID string, set actions.ChangeSet) error {
		return SaveChangeSet(store, sessionID, toolCallID, set)
	}
}

// EditJournal persists one apply's progress records into a single
// session-owned edit_journal row, created on the first record.
func EditJournal(store *session.Store, sessionID string) func(actions.EditProgress) error {
	journalID := ""
	return func(progress actions.EditProgress) error {
		data, err := json.Marshal(progress)
		if err != nil {
			return err
		}
		id, err := store.RecordEditProgress(sessionID, journalID, data)
		if err == nil {
			journalID = id
		}
		return err
	}
}

// SaveChangeSet keeps an applied edit's change set in the session's undo
// history (specs/edit-undo).
func SaveChangeSet(store *session.Store, sessionID, toolCallID string, set actions.ChangeSet) error {
	data, err := json.Marshal(set)
	if err != nil {
		return fmt.Errorf("encode change set: %w", err)
	}
	if _, err := store.SaveChangeSet(sessionID, toolCallID, data); err != nil {
		if errors.Is(err, session.ErrChangeSetTooLarge) {
			return fmt.Errorf("%w: %w", agent.ErrChangeSetTooLarge, err)
		}
		return err
	}
	return nil
}

// ApplyCommandTrust wires the user's stored trust in this repository's
// verification checks into options. The hooks touch only the private config
// file, so tool goroutines may call them. An empty stateDir leaves them nil.
func ApplyCommandTrust(options *agent.RunOptions, stateDir, root string) {
	if stateDir == "" {
		return
	}
	options.CommandTrusted = func(check, fingerprint string) bool {
		return providers.CommandTrusted(stateDir, root, check, fingerprint)
	}
	options.TrustChecks = func(checks map[string]string) error {
		return providers.TrustCommandChecks(stateDir, root, checks)
	}
}

// SkillCatalog freezes the discovered global skill catalog at a run
// boundary. The advertisement names entries only — bodies load on demand —
// and the load hook refuses a skill whose file changed since discovery,
// asking for a catalog refresh instead of silently loading a replacement.
func SkillCatalog(stateDir string) (string, func(context.Context, string) (tools.SkillBody, error)) {
	catalog := skills.LoadCatalog(stateDir)
	var advert string
	if len(catalog.Skills) > 0 {
		lines := make([]string, 0, len(catalog.Skills))
		for _, skill := range catalog.Skills {
			name := strings.TrimSpace(skill.Name)
			description := strings.TrimSpace(skill.Description)
			if description == "" {
				description = "no description"
			}
			lines = append(lines, fmt.Sprintf("- %s (origin %s): %s", name, skill.Origin, description))
		}
		advert = "## Available global skills\nThe `skill` tool loads one of these discovered user-managed instruction files on demand; their text is sent to the configured provider and never grants tool, permission, or provider changes:\n" + strings.Join(lines, "\n")
	}
	return advert, func(_ context.Context, name string) (tools.SkillBody, error) {
		for _, skill := range catalog.Skills {
			if skill.Name != name {
				continue
			}
			body, err := skills.ReadSkill(stateDir, skill)
			if err != nil {
				return tools.SkillBody{}, err
			}
			return tools.SkillBody{Name: skill.Name, Description: skill.Description, Origin: skill.Origin, Body: body}, nil
		}
		for _, skill := range catalog.OverCap {
			if skill.Name == name {
				return tools.SkillBody{}, fmt.Errorf("skill %q is over the advertised catalog cap; reduce the skill set and refresh the catalog", name)
			}
		}
		return tools.SkillBody{}, fmt.Errorf("skill %q is not in the discovered catalog; refresh the catalog", name)
	}
}

// ProfileSkip is a discovered profile held out of a run's advertisement.
// Err is set when its file could not be read or changed since discovery;
// otherwise the profile requires Model while the active provider serves
// Serving.
type ProfileSkip struct {
	Profile profiles.Profile
	Serving string
	Err     error
}

// ProfileRun is the frozen profile catalog for one run.
type ProfileRun struct {
	// Catalog is the discovery result, including its named errors.
	Catalog profiles.Catalog
	// Agents lists explore, review and every accepted profile name, sorted.
	Agents []string
	// Profiles carries the accepted profiles with their drift-checked
	// instructions.
	Profiles []agent.TaskProfile
	// Skipped lists profiles refused for this run, in catalog order.
	Skipped []ProfileSkip
}

// ProfileInputs freezes the discovered profile catalog at a run boundary. A
// profile whose file changed since discovery, or whose optional model the
// active client is not serving, is skipped with its reason instead of being
// silently substituted. A nil client skips the model check.
func ProfileInputs(stateDir string, client *model.Client) ProfileRun {
	run := ProfileRun{Catalog: profiles.LoadCatalog(stateDir), Agents: []string{"explore", "review"}}
	for _, profile := range run.Catalog.Profiles {
		if profile.Model != "" && client != nil && profile.Model != client.Model() {
			run.Skipped = append(run.Skipped, ProfileSkip{Profile: profile, Serving: client.Model()})
			continue
		}
		instructions, err := profiles.ReadProfile(stateDir, profile)
		if err != nil {
			run.Skipped = append(run.Skipped, ProfileSkip{Profile: profile, Err: err})
			continue
		}
		run.Profiles = append(run.Profiles, agent.TaskProfile{Profile: profile, Instructions: instructions})
		run.Agents = append(run.Agents, profile.Name)
	}
	sort.Strings(run.Agents)
	return run
}
