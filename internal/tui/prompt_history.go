package tui

import "lisa/internal/session"

// restoredPromptHistory prefers the dedicated persisted history. For sessions
// saved before that field existed, seed recall from the durable transcript so
// existing users do not start with an empty history after upgrading.
func restoredPromptHistory(snapshot session.Snapshot) []string {
	if len(snapshot.PromptHistory) > 0 {
		return append([]string(nil), snapshot.PromptHistory...)
	}
	var prompts []string
	for _, entry := range snapshot.Entries {
		if (entry.Role == "You" || entry.Role == "user") && entry.Content != "" {
			prompts = append(prompts, entry.Content)
		}
	}
	if len(prompts) > 0 {
		return prompts
	}
	for _, message := range snapshot.History {
		if message.Role == "user" && message.Content != "" {
			prompts = append(prompts, message.Content)
		}
	}
	return prompts
}

// promptHistory navigates submitted prompts while preserving the live draft.
// cursor == len(items) denotes the live-draft slot.
type promptHistory struct {
	items    []string
	cursor   int
	draft    []rune
	hasDraft bool
}

func newPromptHistory(items []string) promptHistory {
	return promptHistory{
		items:  append([]string(nil), items...),
		cursor: len(items),
	}
}

// append records a submitted prompt. Duplicate prompts remain separate entries.
func (h *promptHistory) append(prompt string) {
	h.items = append(h.items, prompt)
	h.resetNavigation()
}

// previous moves toward older prompts. The live input is saved on the first
// move so next can restore it after reaching the newest prompt.
func (h *promptHistory) previous(input []rune) ([]rune, bool) {
	if len(h.items) == 0 {
		return nil, false
	}
	if h.cursor == len(h.items) {
		h.draft = append([]rune(nil), input...)
		h.hasDraft = true
	}
	if h.cursor <= 0 {
		return nil, false
	}
	h.cursor--
	return []rune(h.items[h.cursor]), true
}

// next moves toward newer prompts, then restores the saved live draft.
func (h *promptHistory) next() ([]rune, bool) {
	if h.cursor >= len(h.items) || len(h.items) == 0 {
		return nil, false
	}
	if h.cursor == len(h.items)-1 && !h.hasDraft {
		return nil, false
	}
	h.cursor++
	if h.cursor == len(h.items) {
		draft := append([]rune(nil), h.draft...)
		h.draft = nil
		h.hasDraft = false
		return draft, true
	}
	return []rune(h.items[h.cursor]), true
}

// resetNavigation exits history navigation without changing submitted prompts.
// Call it before mutating a recalled prompt into a new live draft.
func (h *promptHistory) resetNavigation() {
	h.cursor = len(h.items)
	h.draft = nil
	h.hasDraft = false
}

// all returns a copy of the submitted prompts in their original order.
func (h *promptHistory) all() []string {
	return append([]string(nil), h.items...)
}
