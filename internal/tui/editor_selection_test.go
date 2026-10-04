package tui

import "testing"

func assertEditorSelectionCleared(t *testing.T, s *editState) {
	t.Helper()
	if s.selecting || s.selectionAnchor != 0 {
		t.Fatalf("selection = (anchor %d, selecting %t), want cleared", s.selectionAnchor, s.selecting)
	}
}

func assertEditorSelectionBuffer(t *testing.T, input []rune, s *editState, want string, caret int) {
	t.Helper()
	if string(input) != want || s.caret != caret {
		t.Fatalf("buffer/caret = (%q, %d), want (%q, %d)", string(input), s.caret, want, caret)
	}
	assertEditorSelectionCleared(t, s)
}

func TestEditorSelectionRange(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		anchor     int
		caret      int
		selecting  bool
		start, end int
		text       string
	}{
		{name: "inactive", input: "abcd", anchor: 1, caret: 3},
		{name: "forward", input: "abcd", anchor: 1, caret: 3, selecting: true, start: 1, end: 3, text: "bc"},
		{name: "backward", input: "abcd", anchor: 3, caret: 1, selecting: true, start: 1, end: 3, text: "bc"},
		{name: "zero width", input: "abcd", anchor: 2, caret: 2, selecting: true, start: 2, end: 2},
		{name: "unicode rune indices", input: "a界🙂e\u0301z", anchor: 1, caret: 5, selecting: true, start: 1, end: 5, text: "界🙂e\u0301"},
		{name: "newlines and tabs", input: "a\n\tb", anchor: 1, caret: 3, selecting: true, start: 1, end: 3, text: "\n\t"},
		{name: "both endpoints clamp", input: "abcd", anchor: -99, caret: 99, selecting: true, end: 4, text: "abcd"},
		{name: "reversed endpoints clamp", input: "abcd", anchor: 99, caret: -99, selecting: true, end: 4, text: "abcd"},
		{name: "both before buffer", input: "abcd", anchor: -99, caret: -1, selecting: true},
		{name: "both after buffer", input: "abcd", anchor: 99, caret: 100, selecting: true, start: 4, end: 4},
		{name: "empty buffer", anchor: -99, caret: 99, selecting: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := []rune(test.input)
			s := editState{selectionAnchor: test.anchor, caret: test.caret, selecting: test.selecting}
			start, end, ok := s.selectionRange(input)
			if start != test.start || end != test.end || ok != (test.text != "") {
				t.Fatalf("range = (%d, %d, %t), want (%d, %d, %t)", start, end, ok, test.start, test.end, test.text != "")
			}
			if got := s.selectedText(input); got != test.text {
				t.Fatalf("selectedText = %q, want %q", got, test.text)
			}
			if s.selectionAnchor != test.anchor || s.caret != test.caret || s.selecting != test.selecting {
				t.Fatal("reading the selection changed editor state")
			}
		})
	}
}

func TestEditorSelectionSet(t *testing.T) {
	tests := []struct {
		name                  string
		input                 string
		anchor, caret         int
		wantAnchor, wantCaret int
	}{
		{name: "forward", input: "abcd", anchor: 1, caret: 3, wantAnchor: 1, wantCaret: 3},
		{name: "reverse", input: "abcd", anchor: 3, caret: 1, wantAnchor: 3, wantCaret: 1},
		{name: "zero width remains active", input: "abcd", anchor: 2, caret: 2, wantAnchor: 2, wantCaret: 2},
		{name: "clamp endpoints", input: "界🙂", anchor: -10, caret: 10, wantCaret: 2},
		{name: "clamp reversed endpoints", input: "界🙂", anchor: 10, caret: -10, wantAnchor: 2},
		{name: "empty remains active", anchor: 10, caret: -10},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := editState{caret: 99, lastWasYank: true}
			s.setSelection([]rune(test.input), test.anchor, test.caret)
			if !s.selecting || s.selectionAnchor != test.wantAnchor || s.caret != test.wantCaret {
				t.Fatalf("selection = (%d, %d, %t), want (%d, %d, true)", s.selectionAnchor, s.caret, s.selecting, test.wantAnchor, test.wantCaret)
			}
			if s.lastWasYank {
				t.Fatal("setting selection must interrupt a previous yank cycle")
			}
		})
	}
}

func TestEditorSelectionClearOnlyResetsSelection(t *testing.T) {
	s := editState{caret: 7, selectionAnchor: 3, selecting: true, lastWasYank: true, yankIdx: 1, yankLen: 2, ring: [][]rune{[]rune("kill")}}
	s.clearSelection()
	assertEditorSelectionCleared(t, &s)
	if s.caret != 7 || !s.lastWasYank || s.yankIdx != 1 || s.yankLen != 2 || string(s.ring[0]) != "kill" {
		t.Fatal("clearSelection changed caret or kill/yank state")
	}
	s.clearSelection()
	assertEditorSelectionCleared(t, &s)
}

func TestEditorSelectionEndCaretClearsSelection(t *testing.T) {
	s := editState{caret: 1, selectionAnchor: 3, selecting: true, lastWasYank: true}
	s.endCaret([]rune("界🙂"))
	assertEditorSelectionCleared(t, &s)
	if s.caret != 2 || s.lastWasYank {
		t.Fatalf("caret/yank = (%d, %t), want (2, false)", s.caret, s.lastWasYank)
	}
	s.setSelection(nil, 0, 0)
	s.endCaret(nil)
	assertEditorSelectionCleared(t, &s)
	if s.caret != 0 {
		t.Fatalf("empty caret = %d, want 0", s.caret)
	}
}

func TestEditorSelectionExtendReversesAroundOriginalAnchor(t *testing.T) {
	input := []rune("a界🙂bcd")
	s := editState{caret: 3, selectionAnchor: 99, lastWasYank: true}
	for _, caret := range []int{5, 4, 3, 1, 0, 2, 3, 6} {
		s.extendSelection(input, caret)
		if !s.selecting || s.selectionAnchor != 3 || s.caret != caret {
			t.Fatalf("extend to %d = (anchor %d, caret %d, selecting %t), want (3, %d, true)", caret, s.selectionAnchor, s.caret, s.selecting, caret)
		}
		start, end, ok := s.selectionRange(input)
		if start != min(3, caret) || end != max(3, caret) || ok != (caret != 3) {
			t.Fatalf("extend to %d range = (%d, %d, %t)", caret, start, end, ok)
		}
		if s.lastWasYank {
			t.Fatal("extending selection must interrupt a previous yank cycle")
		}
	}
	s.clearSelection()
	s.extendSelection(input, 2)
	if s.selectionAnchor != 6 || s.caret != 2 {
		t.Fatalf("new extension anchor/caret = (%d, %d), want (6, 2)", s.selectionAnchor, s.caret)
	}
}

func TestEditorSelectionExtendClampsOldCaretAndAnchor(t *testing.T) {
	input := []rune("abc")
	s := editState{caret: 99}
	s.extendSelection(input, -10)
	if s.selectionAnchor != 3 || s.caret != 0 {
		t.Fatalf("clamped extension = (%d, %d), want (3, 0)", s.selectionAnchor, s.caret)
	}
	s.selectionAnchor = -99
	s.extendSelection(input, 99)
	if s.selectionAnchor != 0 || s.caret != 3 {
		t.Fatalf("stale anchor extension = (%d, %d), want (0, 3)", s.selectionAnchor, s.caret)
	}
	s = editState{caret: -99}
	s.extendSelection(input, 2)
	if s.selectionAnchor != 0 || s.caret != 2 {
		t.Fatalf("negative old caret extension = (%d, %d), want (0, 2)", s.selectionAnchor, s.caret)
	}
	s.extendSelection(nil, 99)
	if !s.selecting || s.selectionAnchor != 0 || s.caret != 0 {
		t.Fatal("empty-buffer extension must retain a clamped zero-width selection")
	}
}

func TestEditorSelectionInsertReplacesRange(t *testing.T) {
	tests := []struct {
		name          string
		input, insert string
		anchor, caret int
		want          string
		wantCaret     int
	}{
		{name: "forward", input: "abcdef", insert: "XY", anchor: 1, caret: 4, want: "aXYef", wantCaret: 3},
		{name: "reverse", input: "abcdef", insert: "XY", anchor: 4, caret: 1, want: "aXYef", wantCaret: 3},
		{name: "unicode", input: "a界🙂e\u0301z", insert: "é🙂", anchor: 1, caret: 5, want: "aé🙂z", wantCaret: 3},
		{name: "newline", input: "abcd", insert: "\n", anchor: 1, caret: 3, want: "a\nd", wantCaret: 2},
		{name: "zero width", input: "abcd", insert: "X", anchor: 2, caret: 2, want: "abXcd", wantCaret: 3},
		{name: "stale endpoints", input: "abcd", insert: "界", anchor: 99, caret: -99, want: "界", wantCaret: 1},
		{name: "empty buffer", insert: "界🙂", anchor: 99, caret: -99, want: "界🙂", wantCaret: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := []rune(test.input)
			s := editState{caret: test.caret, selectionAnchor: test.anchor, selecting: true, lastWasYank: true}
			insertRunes(&input, &s, []rune(test.insert))
			assertEditorSelectionBuffer(t, input, &s, test.want, test.wantCaret)
			if s.lastWasYank || len(s.ring) != 0 {
				t.Fatal("replacement must interrupt yank without adding to the kill ring")
			}
		})
	}
}

func TestEditorSelectionEmptyInsertIsNoOp(t *testing.T) {
	input := []rune("abcd")
	s := editState{}
	s.setSelection(input, 1, 3)
	insertRunes(&input, &s, nil)
	if string(input) != "abcd" || s.caret != 3 || s.selectionAnchor != 1 || !s.selecting {
		t.Fatal("empty insertion changed input or selection")
	}
}

func TestEditorSelectionInsertNewlineReplacesRange(t *testing.T) {
	input := []rune("a界🙂z")
	s := editState{}
	s.setSelection(input, 3, 1)
	insertNewline(&input, &s)
	assertEditorSelectionBuffer(t, input, &s, "a\nz", 2)
}

func TestEditorSelectionDelete(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(*[]rune, *editState) bool
	}{{"back", deleteBack}, {"forward", deleteForward}} {
		for _, test := range []struct {
			name          string
			anchor, caret int
			want          string
			wantCaret     int
		}{
			{name: "forward selection", anchor: 1, caret: 4, want: "az", wantCaret: 1},
			{name: "backward selection", anchor: 4, caret: 1, want: "az", wantCaret: 1},
			{name: "prefix", anchor: 0, caret: 2, want: "🙂bz", wantCaret: 0},
			{name: "suffix", anchor: 2, caret: 5, want: "a界", wantCaret: 2},
			{name: "clamped whole buffer", anchor: 99, caret: -99, want: "", wantCaret: 0},
		} {
			t.Run(operation.name+"/"+test.name, func(t *testing.T) {
				input := []rune("a界🙂bz")
				s := editState{selectionAnchor: test.anchor, caret: test.caret, selecting: true, lastWasYank: true}
				if !operation.run(&input, &s) {
					t.Fatal("selected deletion did not run")
				}
				assertEditorSelectionBuffer(t, input, &s, test.want, test.wantCaret)
				if s.lastWasYank || len(s.ring) != 0 {
					t.Fatal("deletion must interrupt yank without adding to the kill ring")
				}
			})
		}
	}
}

func TestEditorSelectionZeroWidthDeleteFallsBack(t *testing.T) {
	for _, test := range []struct {
		name      string
		run       func(*[]rune, *editState) bool
		caret     int
		want      string
		wantCaret int
		moved     bool
	}{
		{name: "back", run: deleteBack, caret: 2, want: "acd", wantCaret: 1, moved: true},
		{name: "forward", run: deleteForward, caret: 2, want: "abd", wantCaret: 2, moved: true},
		{name: "back at start", run: deleteBack, want: "abcd"},
		{name: "forward at end", run: deleteForward, caret: 4, want: "abcd", wantCaret: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []rune("abcd")
			s := editState{}
			s.setSelection(input, test.caret, test.caret)
			if got := test.run(&input, &s); got != test.moved {
				t.Fatalf("delete = %t, want %t", got, test.moved)
			}
			assertEditorSelectionBuffer(t, input, &s, test.want, test.wantCaret)
		})
	}
}

func TestEditorSelectionKillsOnlySelectedRange(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(*[]rune, *editState) bool
	}{{"word back", killWordBack}, {"word forward", killWordForward}, {"to start", killToStart}, {"to end", killToEnd}} {
		for _, reversed := range []bool{false, true} {
			t.Run(operation.name+map[bool]string{false: "/forward", true: "/reverse"}[reversed], func(t *testing.T) {
				input := []rune("a界🙂\n\tbz")
				s := editState{selectionAnchor: 1, caret: 5, selecting: true, lastWasYank: true, ring: [][]rune{[]rune("older")}}
				if reversed {
					s.selectionAnchor, s.caret = s.caret, s.selectionAnchor
				}
				if !operation.run(&input, &s) {
					t.Fatal("selected kill did not run")
				}
				assertEditorSelectionBuffer(t, input, &s, "abz", 1)
				if len(s.ring) != 2 || string(s.ring[0]) != "界🙂\n\t" || string(s.ring[1]) != "older" || s.lastWasYank {
					t.Fatalf("kill ring = %q, lastWasYank = %t", s.ring, s.lastWasYank)
				}
				input[1] = 'X'
				if string(s.ring[0]) != "界🙂\n\t" {
					t.Fatal("kill ring aliases the edited input")
				}
				if !yank(&input, &s) {
					t.Fatal("selected kill could not be yanked")
				}
				assertEditorSelectionBuffer(t, input, &s, "a界🙂\n\tXz", 5)
			})
		}
	}
}

func TestEditorSelectionKillsClampEndpoints(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(*[]rune, *editState) bool
	}{{"word back", killWordBack}, {"word forward", killWordForward}, {"to start", killToStart}, {"to end", killToEnd}} {
		t.Run(operation.name, func(t *testing.T) {
			input := []rune("界🙂")
			s := editState{selectionAnchor: 99, caret: -99, selecting: true}
			if !operation.run(&input, &s) {
				t.Fatal("clamped selection kill did not run")
			}
			assertEditorSelectionBuffer(t, input, &s, "", 0)
			if len(s.ring) != 1 || string(s.ring[0]) != "界🙂" {
				t.Fatalf("ring = %q, want [界🙂]", s.ring)
			}
		})
	}
}

func TestEditorSelectionZeroWidthKillsFallBack(t *testing.T) {
	for _, test := range []struct {
		name      string
		run       func(*[]rune, *editState) bool
		caret     int
		want      string
		wantCaret int
		kill      string
	}{
		{name: "word back", run: killWordBack, caret: 7, want: "one", wantCaret: 3, kill: " two"},
		{name: "word forward", run: killWordForward, caret: 3, want: "one", wantCaret: 3, kill: " two"},
		{name: "to start", run: killToStart, caret: 4, want: "two", kill: "one "},
		{name: "to end", run: killToEnd, caret: 3, want: "one", wantCaret: 3, kill: " two"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []rune("one two")
			s := editState{}
			s.setSelection(input, test.caret, test.caret)
			if !test.run(&input, &s) {
				t.Fatal("zero-width fallback kill did not run")
			}
			assertEditorSelectionBuffer(t, input, &s, test.want, test.wantCaret)
			if len(s.ring) != 1 || string(s.ring[0]) != test.kill {
				t.Fatalf("ring = %q, want [%q]", s.ring, test.kill)
			}
		})
	}
}

func TestEditorSelectionEmptyDeletesAndKillsClearZeroWidth(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(*[]rune, *editState) bool
	}{
		{"delete back", deleteBack},
		{"delete forward", deleteForward},
		{"kill word back", killWordBack},
		{"kill word forward", killWordForward},
		{"kill to start", killToStart},
		{"kill to end", killToEnd},
	} {
		t.Run(operation.name, func(t *testing.T) {
			var input []rune
			s := editState{selectionAnchor: 99, caret: -99, selecting: true}
			if operation.run(&input, &s) {
				t.Fatal("delete or kill on an empty buffer must be a no-op")
			}
			assertEditorSelectionBuffer(t, input, &s, "", 0)
			if len(s.ring) != 0 {
				t.Fatal("empty kill added to the ring")
			}
		})
	}
}

func TestEditorSelectionYankReplacesAndRepeatCycles(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		t.Run(map[bool]string{false: "forward", true: "reverse"}[reversed], func(t *testing.T) {
			input := []rune("a界🙂bz")
			s := editState{ring: [][]rune{[]rune("NEW"), []rune("é"), []rune("old🙂")}}
			anchor, caret := 1, 4
			if reversed {
				anchor, caret = caret, anchor
			}
			s.setSelection(input, anchor, caret)
			for i, text := range []string{"NEW", "é", "old🙂", "NEW"} {
				if !yank(&input, &s) {
					t.Fatal("yank did not run")
				}
				assertEditorSelectionBuffer(t, input, &s, "a"+text+"z", 1+len([]rune(text)))
				if !s.lastWasYank || s.yankIdx != i%3 || s.yankLen != len([]rune(text)) {
					t.Fatalf("yank metadata = (%t, %d, %d), want (true, %d, %d)", s.lastWasYank, s.yankIdx, s.yankLen, i%3, len([]rune(text)))
				}
			}
			s.setSelection(input, 0, 1)
			if !yank(&input, &s) {
				t.Fatal("yank with a new selection did not run")
			}
			assertEditorSelectionBuffer(t, input, &s, "NEWNEWz", 3)
			if s.yankIdx != 0 {
				t.Fatal("new selection did not reset the yank cycle")
			}
		})
	}
}

func TestEditorSelectionYankPrefersSelectionOverStaleRepeat(t *testing.T) {
	input := []rune("a界🙂z")
	s := editState{caret: -99, selectionAnchor: 99, selecting: true, lastWasYank: true, yankLen: 20, yankIdx: 1, ring: [][]rune{[]rune("new"), []rune("old")}}
	if !yank(&input, &s) {
		t.Fatal("yank did not run")
	}
	assertEditorSelectionBuffer(t, input, &s, "new", 3)
	if s.yankIdx != 0 || s.yankLen != 3 || !s.lastWasYank {
		t.Fatal("selection replacement did not start a fresh yank cycle")
	}
}

func TestEditorSelectionZeroWidthYankStartsNewCycle(t *testing.T) {
	input := []rune("ab")
	s := editState{caret: 1, ring: [][]rune{[]rune("X"), []rune("Y")}}
	yank(&input, &s)
	s.setSelection(input, s.caret, s.caret)
	if !yank(&input, &s) {
		t.Fatal("zero-width yank did not run")
	}
	assertEditorSelectionBuffer(t, input, &s, "aXXb", 3)
	if s.yankIdx != 0 {
		t.Fatal("zero-width selection did not interrupt the old yank cycle")
	}
}

func TestEditorSelectionYankWithoutRingIsNoOp(t *testing.T) {
	input := []rune("abcd")
	s := editState{}
	s.setSelection(input, 1, 3)
	if yank(&input, &s) {
		t.Fatal("yank without a kill ring must be a no-op")
	}
	if string(input) != "abcd" || s.caret != 3 || s.selectionAnchor != 1 || !s.selecting {
		t.Fatal("failed yank changed input or selection")
	}
}

func TestEditorSelectionYankWithStaleRepeatLengthIsSafe(t *testing.T) {
	input := []rune("a")
	s := editState{caret: 99, lastWasYank: true, yankLen: 20, yankIdx: 1, ring: [][]rune{[]rune("new"), []rune("old")}}
	if !yank(&input, &s) {
		t.Fatal("yank did not run")
	}
	assertEditorSelectionBuffer(t, input, &s, "anew", 4)
	if s.yankIdx != 0 {
		t.Fatal("stale repeat length must start a fresh yank cycle")
	}
}

func TestEditorSelectionWordMovementClears(t *testing.T) {
	input := []rune("one two three")
	for _, test := range []struct {
		name          string
		run           func([]rune, *editState)
		anchor, caret int
		want          int
	}{
		{name: "back", run: moveWordBack, anchor: 0, caret: 7, want: 4},
		{name: "forward", run: moveWordForward, anchor: 8, caret: 4, want: 7},
		{name: "back at start", run: moveWordBack, anchor: 3},
		{name: "forward at end", run: moveWordForward, anchor: 1, caret: len(input), want: len(input)},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := editState{selectionAnchor: test.anchor, caret: test.caret, selecting: true, lastWasYank: true}
			test.run(input, &s)
			assertEditorSelectionBuffer(t, input, &s, string(input), test.want)
			if s.lastWasYank {
				t.Fatal("word movement did not interrupt yank")
			}
		})
	}
}

func TestEditorSelectionTransposeClearsSafely(t *testing.T) {
	for _, test := range []struct {
		name          string
		input, want   string
		anchor, caret int
		wantCaret     int
		changed       bool
	}{
		{name: "selected text", input: "a界🙂z", want: "a🙂界z", anchor: 1, caret: 3, wantCaret: 3, changed: true},
		{name: "stale endpoints", input: "abc", want: "acb", anchor: -99, caret: 99, wantCaret: 3, changed: true},
		{name: "too near start", input: "abc", want: "abc", anchor: 3, caret: 1, wantCaret: 1},
		{name: "empty", anchor: 99, caret: -99},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []rune(test.input)
			s := editState{selectionAnchor: test.anchor, caret: test.caret, selecting: true}
			if got := transpose(&input, &s); got != test.changed {
				t.Fatalf("transpose = %t, want %t", got, test.changed)
			}
			assertEditorSelectionBuffer(t, input, &s, test.want, test.wantCaret)
		})
	}
}
