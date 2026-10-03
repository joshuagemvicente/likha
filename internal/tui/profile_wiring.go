package tui

import (
	"fmt"
	"sort"
	"strings"

	"likha/internal/agent"
	"likha/internal/profiles"
)

// profileRunInputs freezes the discovered profile catalog at a run boundary:
// the task tool advertises explore, review, and every accepted profile name,
// and each accepted profile carries its drift-checked instructions. A profile
// whose file changed since discovery, or whose optional model the active
// provider is not serving, is refused with a visible reason and held out of
// this run's advertisement instead of silently substituted.
func (m *ui) profileRunInputs() ([]string, []agent.TaskProfile) {
	catalog := profiles.LoadCatalog(m.stateDir)
	m.profileCatalog = catalog
	m.reportProfileCatalogIssue(catalog)
	agents := []string{"explore", "review"}
	var taskProfiles []agent.TaskProfile
	for _, profile := range catalog.Profiles {
		if profile.Model != "" && m.client != nil && profile.Model != m.client.Model() {
			m.entries = append(m.entries, entry{role: "Likha", content: fmt.Sprintf("Profile %s requires model %s; the active provider is serving %s, so it is not advertised this run.",
				toolShortText(profile.Name, 48), toolShortText(profile.Model, 96), toolShortText(m.client.Model(), 96))})
			continue
		}
		instructions, err := profiles.ReadProfile(m.stateDir, profile)
		if err != nil {
			m.entries = append(m.entries, entry{role: "Likha", content: "Profile " + toolShortText(profile.Name, 48) + " refused: " + toolShortText(err.Error(), 160) +
				" Refresh the catalog before it is advertised again."})
			continue
		}
		taskProfiles = append(taskProfiles, agent.TaskProfile{Profile: profile, Instructions: instructions})
		agents = append(agents, profile.Name)
	}
	sort.Strings(agents)
	return agents, taskProfiles
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
