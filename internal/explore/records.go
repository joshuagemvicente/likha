package explore

import (
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"likha/internal/model"
	"likha/internal/tools"
)

type recordUpdate struct {
	node   *Node
	record Record
	done   chan<- error
}

// Snapshot returns detached records in accepted creation order. Historical
// records retain their budget charge even after completion or cancellation.
func (m *Manager) Snapshot() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	records := make([]Record, len(m.order))
	for i, n := range m.order {
		records[i] = cloneRecord(n.record)
	}
	return records
}

// updateLocked captures each version before releasing the manager lock. One
// publisher drains these snapshots in order and invokes external code unlocked.
func (m *Manager) updateLocked(n *Node, done chan<- error) {
	n.accountLocked(time.Now())
	n.record.Version++
	n.record.SpawnUsed = m.accepted
	if m.config.OnUpdate == nil {
		if done != nil {
			done <- nil
		}
		return
	}
	m.updates = append(m.updates, recordUpdate{node: n, record: cloneRecord(n.record), done: done})
	if !m.publishing {
		m.publishing = true
		go m.publish()
	}
}

func (m *Manager) publish() {
	for {
		m.mu.Lock()
		if len(m.updates) == 0 {
			m.publishing = false
			m.changed.Broadcast()
			m.mu.Unlock()
			return
		}
		update := m.updates[0]
		m.updates[0] = recordUpdate{}
		m.updates = m.updates[1:]
		m.mu.Unlock()

		err := m.persist(update.record)
		if err != nil {
			m.mu.Lock()
			m.finishLocked(update.node, Failed, "could not persist explore task: "+err.Error())
			m.mu.Unlock()
		}
		if update.done != nil {
			update.done <- err
		}
	}
}

func (m *Manager) persist(record Record) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("explore persistence callback panicked: %v", recovered)
		}
	}()
	return m.config.OnUpdate(record)
}

func cloneScope(scope tools.Scope) tools.Scope {
	if scope.Allowed != nil {
		allowed := make(map[string]bool, len(scope.Allowed))
		for name, enabled := range scope.Allowed {
			allowed[name] = enabled
		}
		scope.Allowed = allowed
	}
	return scope
}

func cloneMessages(messages []model.Message) []model.Message {
	if messages == nil {
		return nil
	}
	cloned := append([]model.Message{}, messages...)
	for i := range cloned {
		if messages[i].ToolCalls != nil {
			cloned[i].ToolCalls = append([]model.ToolCall{}, messages[i].ToolCalls...)
		}
	}
	return cloned
}

func cloneToolRecord(record ToolRecord) ToolRecord {
	// JSON also detaches any result metadata/raw JSON slices in the persisted
	// contract. Executed is deliberately non-JSON runtime metadata.
	if data, err := json.Marshal(record.Result); err == nil {
		var result tools.Result
		if json.Unmarshal(data, &result) == nil {
			result.Executed = record.Result.Executed
			record.Result = result
			return record
		}
	}
	record.Result.Warnings = append([]string(nil), record.Result.Warnings...)
	return record
}

func cloneRecord(record Record) Record {
	record.History = cloneMessages(record.History)
	if record.Tools != nil {
		cloned := make([]ToolRecord, len(record.Tools))
		for i, tool := range record.Tools {
			cloned[i] = cloneToolRecord(tool)
		}
		record.Tools = cloned
	}
	if record.Warnings != nil {
		record.Warnings = append([]string{}, record.Warnings...)
	}
	return record
}

func outcomeFromRecord(record Record, ownSpawns int) Outcome {
	outcome := Outcome{
		TaskID: record.ID, Agent: record.Agent, Depth: record.Depth,
		Status: record.Status, Findings: record.Findings, Reason: record.Reason,
		Warnings: append([]string(nil), record.Warnings...), Rounds: record.Rounds,
		// The model-facing result describes this task's own accepted
		// descendants; shared run-wide budget use stays on the private record
		// for /agents inspection.
		SpawnUsed: ownSpawns, Deadline: record.Deadline, Usage: record.Usage,
	}
	return boundOutcome(outcome)
}

// Bound the actual JSON size, including escaping and metadata, while the
// durable child record keeps the detailed findings and warnings for inspection.
func boundOutcome(outcome Outcome) Outcome {
	const truncationWarning = "Task result was truncated to the inline limit; inspect the saved child record for details."
	truncated := len(outcome.Reason) > 4096 || len(outcome.Warnings) > 32
	outcome.Reason = utf8Prefix(outcome.Reason, 4096)
	for i := range outcome.Warnings {
		truncated = truncated || len(outcome.Warnings[i]) > 1024
		outcome.Warnings[i] = utf8Prefix(outcome.Warnings[i], 1024)
	}
	if len(outcome.Warnings) > 32 {
		outcome.Warnings = outcome.Warnings[:32]
	}
	if truncated {
		outcome.Warnings = append([]string{truncationWarning}, outcome.Warnings...)
	}
	for {
		data, err := json.Marshal(outcome)
		if err != nil || len(data) <= tools.MaxInlineBytes {
			return outcome
		}
		if !truncated {
			truncated = true
			outcome.Warnings = append([]string{truncationWarning}, outcome.Warnings...)
		}
		if len(outcome.Findings) > 0 {
			outcome.Findings = utf8Prefix(outcome.Findings, len(outcome.Findings)*3/4)
		} else if len(outcome.Warnings) > 0 {
			outcome.Warnings = outcome.Warnings[:len(outcome.Warnings)/2]
		} else {
			outcome.Reason = utf8Prefix(outcome.Reason, len(outcome.Reason)*3/4)
		}
	}
}

func utf8Prefix(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}
