package tui

import "testing"

func TestComposerVisualRowWrapsByDisplayWidth(t *testing.T) {
	tests := []struct {
		name  string
		input string
		caret int
		width int
		want  int
	}{
		{name: "first row", input: "abcdef", caret: 2, width: 3, want: 0},
		{name: "at automatic wrap", input: "abcdef", caret: 3, width: 3, want: 1},
		{name: "caret after exact-width input wraps", input: "abc", caret: 3, width: 3, want: 1},
		{name: "wide rune", input: "A界B", caret: 2, width: 3, want: 1},
		{name: "wide rune wider than row", input: "界a", caret: 1, width: 1, want: 1},
		{name: "explicit newline before", input: "a\nb", caret: 1, width: 4, want: 0},
		{name: "caret before newline after full row", input: "abc\nx", caret: 3, width: 3, want: 1},
		{name: "explicit newline after", input: "a\nb", caret: 2, width: 4, want: 1},
		{name: "caret before wide rune with room for marker", input: "ab界", caret: 2, width: 3, want: 0},
		{name: "trailing newline", input: "a\n", caret: 2, width: 4, want: 1},
		{name: "empty", input: "", caret: 0, width: 4, want: 0},
		{name: "zero width uses one column", input: "ab", caret: 1, width: 0, want: 1},
		{name: "clamp before input", input: "a\nb", caret: -20, width: 4, want: 0},
		{name: "clamp after input", input: "a\nb", caret: 20, width: 4, want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := composerVisualRow([]rune(test.input), test.caret, test.width); got != test.want {
				t.Fatalf("composerVisualRow(%q, %d, %d) = %d, want %d", test.input, test.caret, test.width, got, test.want)
			}
		})
	}
}

func TestMoveComposerCaretVerticalAcrossWrappedRows(t *testing.T) {
	input := []rune("abcdefghi")
	caret, moved := moveComposerCaretVertical(input, 2, 3, 1)
	if !moved || caret != 5 {
		t.Fatalf("down from column 2 = (%d, %t), want (5, true)", caret, moved)
	}
	caret, moved = moveComposerCaretVertical(input, caret, 3, 1)
	if !moved || caret != 8 {
		t.Fatalf("second down = (%d, %t), want (8, true)", caret, moved)
	}
	caret, moved = moveComposerCaretVertical(input, caret, 3, -1)
	if !moved || caret != 5 {
		t.Fatalf("up = (%d, %t), want (5, true)", caret, moved)
	}
	caret, moved = moveComposerCaretVertical(input, 0, 3, -1)
	if moved || caret != 0 {
		t.Fatalf("up at first row = (%d, %t), want (0, false)", caret, moved)
	}
	caret, moved = moveComposerCaretVertical(input, len(input), 3, 1)
	if moved || caret != len(input) {
		t.Fatalf("down at last row = (%d, %t), want (%d, false)", caret, moved, len(input))
	}
}

func TestMoveComposerCaretVerticalAcrossExplicitNewlines(t *testing.T) {
	input := []rune("abc\nx\nabc")
	caret, moved := moveComposerCaretVertical(input, 2, 8, 1)
	if !moved || caret != 5 {
		t.Fatalf("down to short line = (%d, %t), want (5, true)", caret, moved)
	}
	caret, moved = moveComposerCaretVertical(input, caret, 8, 1)
	if !moved || caret != 7 {
		t.Fatalf("down from short line = (%d, %t), want (7, true)", caret, moved)
	}
}

func TestComposerVerticalMoverRetainsPreferredColumn(t *testing.T) {
	input := []rune("abcdef\nx\nabcdef")
	var mover composerVerticalMover

	caret, moved := mover.move(input, 5, 8, 1)
	if !moved || caret != 8 {
		t.Fatalf("down to short line = (%d, %t), want (8, true)", caret, moved)
	}
	caret, moved = mover.move(input, caret, 8, 1)
	if !moved || caret != 14 {
		t.Fatalf("down from short line should restore column 5 = (%d, %t), want (14, true)", caret, moved)
	}
	caret, moved = mover.move(input, caret, 8, -1)
	if !moved || caret != 8 {
		t.Fatalf("up to short line = (%d, %t), want (8, true)", caret, moved)
	}
	caret, moved = mover.move(input, caret, 8, -1)
	if !moved || caret != 5 {
		t.Fatalf("up should restore column 5 = (%d, %t), want (5, true)", caret, moved)
	}

	mover.reset()
	caret, moved = mover.move(input, 8, 8, 1)
	if !moved || caret != 10 {
		t.Fatalf("after reset, down should use column 1 = (%d, %t), want (10, true)", caret, moved)
	}
}

func TestMoveComposerCaretVerticalHandlesWideRunesAndClamps(t *testing.T) {
	input := []rune("a界bcdef")
	caret, moved := moveComposerCaretVertical(input, 1, 3, 1)
	if !moved || caret != 3 {
		t.Fatalf("down from wide rune row = (%d, %t), want (3, true)", caret, moved)
	}

	caret, moved = moveComposerCaretVertical(input, -5, 3, 1)
	if !moved || caret != 2 {
		t.Fatalf("move from clamped caret = (%d, %t), want (2, true)", caret, moved)
	}
	caret, moved = moveComposerCaretVertical(input, 100, 3, 1)
	if moved || caret != len(input) {
		t.Fatalf("clamped end move = (%d, %t), want (%d, false)", caret, moved, len(input))
	}
	caret, moved = moveComposerCaretVertical(nil, 10, 0, 1)
	if moved || caret != 0 {
		t.Fatalf("empty input move = (%d, %t), want (0, false)", caret, moved)
	}
}

func TestComposerVisualRowMatchesEscapedZeroWidthRunes(t *testing.T) {
	// view.wrap renders a combining mark as a visible escape rather than a
	// zero-cell rune, and wraps those escape characters like ordinary text.
	input := []rune("a\u0301b")
	if got := composerVisualRow(input, 2, 3); got != 2 {
		t.Fatalf("row after escaped combining mark = %d, want 2", got)
	}
}
