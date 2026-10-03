package tui

import (
	"encoding/json"

	"likha/internal/explore"
)

// maxProfileNameRunes mirrors the shared profile-name rule from the
// user-agents spec: 1–64 characters. Every name that passes validProfileName
// is ASCII, so bytes and runes agree here.
const maxProfileNameRunes = 64

// taskProfileFromArguments extracts the requested agent profile name from a
// task call's arguments for display attribution. The arguments are
// model-controlled and untrusted: only a name that already satisfies the
// shared profile-name rule (lowercase a-z and digits with interior hyphens,
// at most 64 runes) is returned, so forged or malformed arguments never
// reach the display. Invalid JSON, a missing or non-string agent field, and
// any suspicious name return "" and the caller keeps the existing row
// content. Dispatch validation stays the runtime's job; this only decides
// whether a name is safe to show.
func taskProfileFromArguments(arguments string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(arguments), &fields) != nil {
		return ""
	}
	raw, ok := fields["agent"]
	if !ok {
		return ""
	}
	var name string
	if json.Unmarshal(raw, &name) != nil {
		return ""
	}
	if !validProfileName(name) {
		return ""
	}
	return name
}

// validProfileName mirrors the shared skills/profile name rule: lowercase
// a-z and digits with interior hyphens only (no leading, trailing, or
// doubled hyphen), at most maxProfileNameRunes, never empty. It matches
// validSkillName in internal/skills so a displayed name is always a name
// discovery could have accepted.
func validProfileName(name string) bool {
	if name == "" || len(name) > maxProfileNameRunes {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || name[i-1] == '-' {
				return false
			}
		default:
			return false
		}
	}
	// The loop checks each hyphen against the byte before it, so a trailing
	// hyphen slips past it; reject that separately.
	return name[len(name)-1] != '-'
}

// taskRowContentForProfile returns the display content for the queued task
// row created at tool_start, attributed to the requested profile. It follows
// the accepted-row format from acceptTaskRecord ("Task %s · depth %d · %s")
// with the profile name where that row shows the description; the task ID
// and depth are not known until acceptance, so the bounded call ID stands
// in. The second return is false when there is no matching unaccepted row —
// for example once acceptTaskRecord has bound the row to a task record — and
// the caller keeps the existing content.
func (m *ui) taskRowContentForProfile(callID, profile string) (string, bool) {
	if callID == "" || profile == "" {
		return "", false
	}
	for i := len(m.toolRecords) - 1; i >= 0; i-- {
		row := &m.toolRecords[i]
		if row.Name == "task" && row.CallID == callID && row.TaskID == "" {
			content := "Task " + toolShortText(callID, 32) +
				" · " + toolShortText(profile, maxProfileNameRunes) +
				" · awaiting acceptance"
			return content, true
		}
	}
	return "", false
}

// profileAttributionNote returns the one-line suffix appended to the Agent
// transcript entry when a task record arrives, attributing the task to a
// non-default profile. The built-in explore role keeps today's wording, so
// this returns "" for explore records and for records carrying no agent
// name. The name is bounded with the shared safe-render helper before it
// reaches the transcript.
func (m *ui) profileAttributionNote(record explore.Record) string {
	if record.Agent == "" || record.Agent == "explore" {
		return ""
	}
	return "profile " + toolShortText(record.Agent, maxProfileNameRunes)
}
