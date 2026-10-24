package tui

import (
	"fmt"

	"likha/internal/model"
)

// queuedSelection is transient execution state, parallel to the existing text
// queue. It is never part of a session snapshot or persisted prompt history.
type queuedSelection struct {
	client    *model.Client
	selection model.Selection
	key       string
	sent      bool
	window    int64
}

func (m *ui) captureSelection() (queuedSelection, error) {
	client, err := m.client.Fork()
	if err != nil {
		return queuedSelection{}, err
	}
	return queuedSelection{client: client, selection: m.composerSelection(), key: client.ExecutionKey(), window: m.contextWindow}, nil
}

func (m *ui) dropQueueHead(n int) {
	n = min(n, len(m.queue))
	m.queue = m.queue[n:]
	if len(m.queueSelections) >= n {
		m.queueSelections = m.queueSelections[n:]
	} else {
		m.queueSelections = nil
	}
}

func (m *ui) queueCanSteer() bool {
	for _, item := range m.queueSelections {
		if !item.sent {
			return false
		}
	}
	return true
}

// refreshRateUsage keeps the newest Codex rate-limit summary. Each run
// streams on a fork whose rate state starts fresh (model.Client.Fork), so the
// composer client never sees the response headers; the summary is copied from
// the run's client as its events arrive and outlives the run.
func (m *ui) refreshRateUsage() {
	if m.runClient == nil {
		return
	}
	if usage := m.runClient.Usage(); usage != "" {
		m.rateUsage = usage
	}
}

// rateLimitUsage is the rate-limit summary the status line shows.
func (m *ui) rateLimitUsage() string {
	if m.rateUsage != "" {
		return m.rateUsage
	}
	if m.client == nil {
		return ""
	}
	return m.client.Usage()
}

// clientUsable reports whether a captured client can still send. Forking is
// side-effect free and refuses exactly when the client's ChatGPT session has
// been invalidated or lost its credentials.
func clientUsable(client *model.Client) bool {
	_, err := client.Fork()
	return err == nil
}

// adoptReplacedClient settles state tied to the previous composer client
// after an idle swap that invalidated it. Its rate window may describe
// another account, and held prompts captured on it share its ChatGPT session,
// so they can no longer send. Their exact selection cannot be rebuilt, so they
// take the current composer selection with a visible note rather than failing
// with a sign-in error when Enter sends them.
func (m *ui) adoptReplacedClient() {
	m.rateUsage = ""
	if !m.working {
		m.runClient = nil
	}
	if m.client == nil {
		return
	}
	moved := 0
	for i, item := range m.queueSelections {
		if item.client == nil || clientUsable(item.client) {
			continue
		}
		fresh, err := m.captureSelection()
		if err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Held queued prompts cannot use the new selection: " + err.Error()})
			return
		}
		m.queueSelections[i] = fresh
		moved++
	}
	if moved == 0 {
		return
	}
	noun := "prompt"
	if moved > 1 {
		noun = "prompts"
	}
	m.entries = append(m.entries, entry{role: "Likha", content: fmt.Sprintf("The ChatGPT session the held queue was captured with ended; %d held queued %s will run with %s.", moved, noun, m.composerSelection())})
	m.layoutWidth = 0
}
