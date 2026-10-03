package tui

import (
	"context"
	"fmt"
	"strings"

	"likha/internal/skills"
	"likha/internal/tools"
)

// skillCatalog freezes the discovered global skill catalog at a run boundary.
// The advertisement names entries only — bodies load on demand — and the
// load hook refuses a skill whose file changed since discovery, asking for a
// catalog refresh instead of silently loading a replacement. Catalog errors
// are reported through reportSkillCatalogIssue, never here, so they do not
// repeat on every turn.
func (m *ui) skillCatalog() (string, func(context.Context, string) (tools.SkillBody, error)) {
	catalog := skills.LoadCatalog(m.stateDir)
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
			body, err := skills.ReadSkill(m.stateDir, skill)
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

func skillCatalogIssue(catalog skills.Catalog) string {
	if len(catalog.OverCap) > 0 {
		over := make([]string, 0, len(catalog.OverCap))
		for _, skill := range catalog.OverCap {
			over = append(over, toolShortText(skill.Name, 48))
		}
		return fmt.Sprintf("Skill catalog over cap; not advertised: %s. Named errors: %s", strings.Join(over, ", "), strings.Join(catalog.Errors, "; "))
	}
	return "Skill catalog: " + strings.Join(catalog.Errors, "; ")
}

// reportSkillCatalogIssue surfaces named discovery problems once per state:
// over the cap, or the newest error not yet reported. It never blocks startup.
func (m *ui) reportSkillCatalogIssue() {
	catalog := skills.LoadCatalog(m.stateDir)
	if len(catalog.Errors) == 0 {
		return
	}
	key := strings.Join(catalog.Errors, "\n")
	if m.reportedSkillErrors == key {
		return
	}
	m.reportedSkillErrors = key
	m.entries = append(m.entries, entry{role: "Likha", content: skillCatalogIssue(catalog)})
}

// loadUserSkill resolves one invoked skill at the user-invocation boundary,
// reading safe metadata fresh and refusing a file that changed since that
// discovery instead of loading an unexpected replacement.
func (m *ui) loadUserSkill(name string) (string, string, error) {
	catalog := skills.LoadCatalog(m.stateDir)
	for _, skill := range catalog.Skills {
		if skill.Name == name {
			body, err := skills.ReadSkill(m.stateDir, skill)
			if err != nil {
				return "", "", err
			}
			return body, skill.Origin, nil
		}
	}
	for _, skill := range catalog.OverCap {
		if skill.Name == name {
			return "", "", fmt.Errorf("skill %q is over the advertised catalog cap; reduce the set and refresh the catalog", name)
		}
	}
	return "", "", fmt.Errorf("skill %q is not in the discovered catalog; /skills lists the current set", name)
}
func (m *ui) describeSkills() string {
	catalog := skills.LoadCatalog(m.stateDir)
	var builder strings.Builder
	if len(catalog.Skills) == 0 && len(catalog.OverCap) == 0 && len(catalog.Errors) == 0 {
		builder.WriteString("No skills discovered. Place one skill per directory in the private skills folder as <name>/SKILL.md with name and description frontmatter.")
		return builder.String()
	}
	if len(catalog.Skills) == 0 {
		builder.WriteString("No skills are advertised.")
	} else {
		builder.WriteString(fmt.Sprintf("%d skill(s) advertised (origin global); load on demand with /skill <name> [request]:", len(catalog.Skills)))
		for _, skill := range catalog.Skills {
			builder.WriteString(fmt.Sprintf("\n- %s: %s", skill.Name, skill.Description))
		}
	}
	if len(catalog.OverCap) > 0 {
		builder.WriteString("\n" + skillCatalogIssue(catalog))
		for _, skill := range catalog.OverCap {
			builder.WriteString("\n- over cap: " + skill.Name)
		}
	}
	if len(catalog.Errors) > 0 {
		builder.WriteString("\nNamed catalog errors (bad identities were skipped, never hidden):")
		for _, err := range catalog.Errors {
			builder.WriteString("\n- " + err)
		}
	}
	return builder.String()
}
