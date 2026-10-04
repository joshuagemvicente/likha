package session

import "slices"

// ToolRecord is durable display metadata for a tool result. EntryIndex is the
// zero-based index in Snapshot.Entries; keeping it separate preserves Entry's
// legacy shape. Arguments describe the call, not configuration credentials or
// permission grants. ArtifactID and continuation fields are references only:
// loading a record never retrieves output or reruns its originating operation.
// Diff is the unified diff the user reviewed and approved for an applied edit
// (edit, edit_file); it is display data for the transcript, absent on every
// other record and on records saved before it existed.
type ToolRecord struct {
	EntryIndex int      `json:"entry_index"`
	CallID     string   `json:"call_id,omitempty"`
	TaskID     string   `json:"task_id,omitempty"`
	Name       string   `json:"name"`
	Arguments  string   `json:"arguments,omitempty"`
	SourceKind string   `json:"source_kind,omitempty"`
	Server     string   `json:"server,omitempty"`
	SourceTool string   `json:"source_tool,omitempty"`
	Status     string   `json:"status"`
	Content    string   `json:"content,omitempty"`
	ArtifactID string   `json:"artifact_id,omitempty"`
	Cursor     string   `json:"cursor,omitempty"`
	Truncated  bool     `json:"truncated,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	NextOffset int      `json:"next_offset,omitempty"`
	Diff       string   `json:"diff,omitempty"`
}

// Record updates are conversation activity; nil and empty collections remain
// equivalent, matching the other durable fields compared by sameConversation.
func sameToolRecords(a, b []ToolRecord) bool {
	return slices.EqualFunc(a, b, func(left, right ToolRecord) bool {
		if left.EntryIndex != right.EntryIndex || left.CallID != right.CallID || left.TaskID != right.TaskID || left.Name != right.Name || left.Arguments != right.Arguments {
			return false
		}
		if left.SourceKind != right.SourceKind || left.Server != right.Server || left.SourceTool != right.SourceTool || left.Status != right.Status {
			return false
		}
		if left.Content != right.Content || left.ArtifactID != right.ArtifactID || left.Cursor != right.Cursor || left.Truncated != right.Truncated || left.NextOffset != right.NextOffset || left.Diff != right.Diff {
			return false
		}
		return slices.Equal(left.Warnings, right.Warnings)
	})
}
