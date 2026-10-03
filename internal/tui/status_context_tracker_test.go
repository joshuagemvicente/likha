package tui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
	"lisa/internal/providers"
)

func TestContextSegmentRendersMeasuredAndEstimatedInput(t *testing.T) {
	m := statusTestUI(t, "minimal", 100, 24, providers.StoredStatusLineConfig{})
	m.contextSeen = true
	m.contextWindowKnown = true
	m.contextWindow = 200_000

	m.contextTokens = 68_000
	if got := m.contextSegment().text; got != "ctx 68k/200k · 34%" {
		t.Fatalf("measured context = %q", got)
	}
	m.contextTokens = 0
	if got := m.contextSegment().text; got != "ctx 0/200k · 0%" {
		t.Fatalf("zero measured context = %q", got)
	}

	m.contextTokens = 42_000
	m.contextEstimated = true
	if got := m.contextSegment().text; got != "ctx ~42k/200k · ~21%" {
		t.Fatalf("estimated context = %q", got)
	}
}

func TestContextSegmentKeepsUnknownLimitsHonest(t *testing.T) {
	m := statusTestUI(t, "minimal", 100, 24, providers.StoredStatusLineConfig{})
	m.contextSeen = true
	m.contextTokens = 42_000
	if got := m.contextSegment().text; got != "ctx 42k/?" {
		t.Fatalf("measured context without a limit = %q", got)
	}

	m.contextEstimated = true
	if got := m.contextSegment().text; got != "ctx ~42k/?" {
		t.Fatalf("estimated context without a limit = %q", got)
	}
	m.contextWindowKnown = true
	if got := m.contextSegment().text; got != "ctx ~42k/?" {
		t.Fatalf("estimated context with an invalid limit = %q", got)
	}

	// Legacy cumulative usage must not be used as a substitute for a current
	// context count when the tracker has no usable value.
	m.contextSeen = false
	m.usageSeen = true
	m.lastPromptTokens = 68_000
	if got := m.contextSegment().text; got != "ctx —" {
		t.Fatalf("missing context count rendered %q", got)
	}

	m.contextSeen = true
	m.contextTokens = -1
	if got := m.contextSegment().text; got != "ctx —" {
		t.Fatalf("invalid context count rendered %q", got)
	}
}

func TestContextSegmentWarningThresholdForMeasuredAndEstimated(t *testing.T) {
	m := statusTestUI(t, "minimal", 100, 24, providers.StoredStatusLineConfig{})
	m.contextSeen = true
	m.contextWindowKnown = true
	m.contextWindow = 200_000
	for _, estimated := range []bool{false, true} {
		m.contextEstimated = estimated
		m.contextTokens = 160_000
		segment := m.contextSegment()
		if segment.style.Render(segment.text) != m.theme.Warning.Render(segment.text) {
			t.Errorf("estimated=%t context at 80%% did not use the warning role: %q", estimated, segment.text)
		}

		m.contextTokens = 158_000
		segment = m.contextSegment()
		if segment.style.Render(segment.text) != m.theme.Normal.Render(segment.text) {
			t.Errorf("estimated=%t context below 80%% did not use the normal role: %q", estimated, segment.text)
		}
	}
}

func TestNarrowContextKeepsCountMarkersAndUnknownLimit(t *testing.T) {
	m := statusTestUI(t, "minimal", 40, 12, providers.StoredStatusLineConfig{})
	m.contextSeen = true
	m.contextEstimated = true
	m.contextTokens = 42_000
	m.contextWindowKnown = true
	m.contextWindow = 200_000

	if got := m.contextSegment().text; got != "ctx ~42k/200k" {
		t.Fatalf("narrow context = %q", got)
	}
	rows := m.statusLineRows(1, 1)
	row := stripANSI(rows[0])
	if !strings.Contains(row, "ctx ~42k/200k") || strings.Contains(row, "~21%") {
		t.Fatalf("narrow status lost count/marker or kept percentage: %q", row)
	}
	if width := runewidth.StringWidth(row); width > m.width {
		t.Fatalf("narrow status width = %d, want <= %d: %q", width, m.width, row)
	}

	m.contextWindowKnown = false
	if got := m.contextSegment().text; got != "ctx ~42k/?" {
		t.Fatalf("narrow context with unknown limit = %q", got)
	}
}
