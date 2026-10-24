package tui

import (
	"fmt"
	"strings"

	"likha/internal/agent"
	"likha/internal/profiles"
	"likha/internal/runsetup"
)

// profileRunInputs freezes the discovered profile catalog at a run boundary:
// the task tool advertises explore, review, and every accepted profile name,
// and each accepted profile carries its drift-checked instructions. A profile
// whose file changed since discovery, or whose optional model the active
// provider is not serving, is refused with a visible reason and held out of
// this run's advertisement instead of silently substituted.
func (m *ui) profileRunInputs() ([]string, []agent.TaskProfile) {
	run := runsetup.ProfileInputs(m.stateDir, m.client)
	m.profileCatalog = run.Catalog
	m.reportProfileCatalogIssue(run.Catalog)
	for _, skip := range run.Skipped {
		if skip.Err != nil {
			m.entries = append(m.entries, entry{role: "Likha", content: "Profile " + toolShortText(skip.Profile.Name, 48) + " refused: " + toolShortText(skip.Err.Error(), 160) +
				" Refresh the catalog before it is advertised again."})
			continue
		}
		m.entries = append(m.entries, entry{role: "Likha", content: fmt.Sprintf("Profile %s requires model %s; the active provider is serving %s, so it is not advertised this run.",
			toolShortText(skip.Profile.Name, 48), toolShortText(skip.Profile.Model, 96), toolShortText(skip.Serving, 96))})
	}
	return run.Agents, run.Profiles
}

// reportProfileCatalogIssue surfaces named discovery problems once per state,
// mirroring the skill catalog rule: named errors, never hidden, never repeated
// on every turn.
func (m *ui) reportProfileCatalogIssue(catalog profiles.Catalog) {
	if len(catalog.Errors) == 0 {
		return
	}
	key := strings.Join(catalog.Errors, "\n")
	if m.reportedProfileErrors == key {
		return
	}
	m.reportedProfileErrors = key
	m.entries = append(m.entries, entry{role: "Likha", content: "Profile catalog: " + strings.Join(catalog.Errors, "; ")})
}

// refreshProfileCatalog re-reads safe metadata for the /agents listing at idle
// boundaries; the run catalog itself freezes inside profileRunInputs.
func (m *ui) refreshProfileCatalog() {
	m.profileCatalog = profiles.LoadCatalog(m.stateDir)
}
