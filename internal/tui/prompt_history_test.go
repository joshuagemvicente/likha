package tui

import (
	"reflect"
	"testing"
)

func TestPromptHistoryPreviousNextRestoresDraft(t *testing.T) {
	h := newPromptHistory([]string{"older", "newer"})
	live := []rune("current draft")

	got, ok := h.previous(live)
	if !ok || string(got) != "newer" {
		t.Fatalf("previous() = (%q, %v), want (%q, true)", string(got), ok, "newer")
	}
	got, ok = h.previous(nil)
	if !ok || string(got) != "older" {
		t.Fatalf("previous() = (%q, %v), want (%q, true)", string(got), ok, "older")
	}
	if got, ok = h.previous(nil); ok || got != nil {
		t.Fatalf("previous() at oldest = (%q, %v), want (nil, false)", string(got), ok)
	}

	got, ok = h.next()
	if !ok || string(got) != "newer" {
		t.Fatalf("next() = (%q, %v), want (%q, true)", string(got), ok, "newer")
	}
	got, ok = h.next()
	if !ok || string(got) != "current draft" {
		t.Fatalf("next() = (%q, %v), want (%q, true)", string(got), ok, "current draft")
	}
	if got, ok = h.next(); ok || got != nil {
		t.Fatalf("next() at live slot = (%q, %v), want (nil, false)", string(got), ok)
	}
}

func TestPromptHistoryCopiesInputsAndOutputs(t *testing.T) {
	items := []string{"saved prompt"}
	h := newPromptHistory(items)
	items[0] = "changed source"

	live := []rune("draft")
	got, ok := h.previous(live)
	if !ok || string(got) != "saved prompt" {
		t.Fatalf("previous() = (%q, %v), want saved prompt", string(got), ok)
	}
	got[0] = 'X'
	live[0] = 'X'

	got, ok = h.next()
	if !ok || string(got) != "draft" {
		t.Fatalf("next() = (%q, %v), want original draft", string(got), ok)
	}
	got[0] = 'X'

	all := h.all()
	if !reflect.DeepEqual(all, []string{"saved prompt"}) {
		t.Fatalf("all() = %#v, want original history", all)
	}
	all[0] = "changed copy"
	if got := h.all(); !reflect.DeepEqual(got, []string{"saved prompt"}) {
		t.Fatalf("mutating all() result changed history: %#v", got)
	}
}

func TestPromptHistoryEmptyValuesAndNoHistory(t *testing.T) {
	empty := newPromptHistory(nil)
	if got, ok := empty.previous([]rune("draft")); ok || got != nil {
		t.Fatalf("previous() with no items = (%q, %v), want (nil, false)", string(got), ok)
	}
	if got, ok := empty.next(); ok || got != nil {
		t.Fatalf("next() with no items = (%q, %v), want (nil, false)", string(got), ok)
	}

	h := newPromptHistory([]string{"", "same", "same"})
	got, ok := h.previous(nil)
	if !ok || string(got) != "same" {
		t.Fatalf("previous() = (%q, %v), want newest duplicate", string(got), ok)
	}
	got, ok = h.previous(nil)
	if !ok || string(got) != "same" {
		t.Fatalf("previous() = (%q, %v), want older duplicate", string(got), ok)
	}
	got, ok = h.previous(nil)
	if !ok || len(got) != 0 {
		t.Fatalf("previous() for empty prompt = (%q, %v), want empty prompt and true", string(got), ok)
	}
	for range 2 {
		if _, ok := h.next(); !ok {
			t.Fatal("next() unexpectedly failed while walking newer prompts")
		}
	}
	if got, ok = h.next(); !ok || len(got) != 0 {
		t.Fatalf("next() for empty live draft = (%q, %v), want empty draft and true", string(got), ok)
	}
}

func TestPromptHistoryResetNavigationKeepsHistoryAndUsesNewDraft(t *testing.T) {
	h := newPromptHistory([]string{"older", "newer"})
	if got, ok := h.previous([]rune("stale draft")); !ok || string(got) != "newer" {
		t.Fatalf("previous() = (%q, %v), want newer", string(got), ok)
	}
	h.resetNavigation()

	if got := h.all(); !reflect.DeepEqual(got, []string{"older", "newer"}) {
		t.Fatalf("resetNavigation changed history: %#v", got)
	}
	if got, ok := h.next(); ok || got != nil {
		t.Fatalf("next() after reset = (%q, %v), want (nil, false)", string(got), ok)
	}
	if got, ok := h.previous([]rune("edited draft")); !ok || string(got) != "newer" {
		t.Fatalf("previous() after reset = (%q, %v), want newer", string(got), ok)
	}
	if got, ok := h.next(); !ok || string(got) != "edited draft" {
		t.Fatalf("next() after reset = (%q, %v), want edited draft", string(got), ok)
	}
}

func TestPromptHistoryAppendKeepsDuplicatesAndResetsNavigation(t *testing.T) {
	h := newPromptHistory([]string{"repeat"})
	if _, ok := h.previous([]rune("draft")); !ok {
		t.Fatal("previous() unexpectedly failed")
	}
	h.append("repeat")

	if got, want := h.all(), []string{"repeat", "repeat"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("all() = %#v, want %#v", got, want)
	}
	if got, ok := h.next(); ok || got != nil {
		t.Fatalf("next() after append = (%q, %v), want (nil, false)", string(got), ok)
	}
	if got, ok := h.previous(nil); !ok || string(got) != "repeat" {
		t.Fatalf("previous() after append = (%q, %v), want newest duplicate", string(got), ok)
	}
}
