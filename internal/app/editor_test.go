package app

import (
	"strings"
	"testing"
)

// Editor-state unit tests for specs/tool-rendering-terminal-keys M2: cursor,
// word kills with whitespace-run collapsing, motion, line kills, yank ring,
// transpose. The word rule is plain space/tab/newline.

func runeEq(t *testing.T, got []rune, want string) {
	t.Helper()
	if string(got) != want {
		t.Fatalf("buffer = %q, want %q", string(got), want)
	}
}

func TestInsertAtCaret(t *testing.T) {
	input := []rune("abf")
	s := editState{caret: 2}
	insertRunes(&input, &s, []rune("cd"))
	runeEq(t, input, "abcdf")
	if s.caret != 4 {
		t.Fatalf("caret = %d, want 4", s.caret)
	}
	// Caret clamps on out-of-range state.
	input = []rune("ab")
	s = editState{caret: 99}
	insertRunes(&input, &s, []rune("x"))
	runeEq(t, input, "abx")
}

func TestDeleteBackAndForward(t *testing.T) {
	input := []rune("abc")
	s := editState{caret: 2}
	if !deleteBack(&input, &s) {
		t.Fatal("deleteBack did not run")
	}
	runeEq(t, input, "ac")
	if s.caret != 1 {
		t.Fatalf("caret = %d, want 1", s.caret)
	}
	if !deleteForward(&input, &s) {
		t.Fatal("deleteForward did not run")
	}
	runeEq(t, input, "a")
	if !deleteBack(&input, &s) {
		t.Fatal("deleteBack did not run")
	}
	runeEq(t, input, "")
	if deleteBack(&input, &s) {
		t.Fatal("deleteBack at empty buffer must be a no-op")
	}
	if deleteForward(&input, &s) {
		t.Fatal("deleteForward at end of buffer must be a no-op")
	}
}

func TestKillWordBackCollapsesWhitespaceRun(t *testing.T) {
	// The previous word dies together with its preceding whitespace run in
	// one keystroke (FR-17's whitespace-run rule).
	input := []rune("foo    bar")
	s := editState{caret: len(input)}
	if !killWordBack(&input, &s) {
		t.Fatal("killWordBack did not run")
	}
	runeEq(t, input, "foo")
	if s.caret != 3 {
		t.Fatalf("caret = %d, want 3", s.caret)
	}
	// A caret sitting in whitespace: the run itself is the kill.
	input = []rune("a   ")
	s = editState{caret: len(input)}
	if !killWordBack(&input, &s) {
		t.Fatal("whitespace-run kill did not run")
	}
	runeEq(t, input, "a")
	// Empty buffer is a no-op.
	input = []rune("")
	s = editState{caret: 0}
	if killWordBack(&input, &s) {
		t.Fatal("killWordBack at empty buffer must be a no-op")
	}
}

func TestKillWordForwardMirrorsBack(t *testing.T) {
	// The next word dies together with its trailing whitespace run.
	input := []rune("word two  three")
	s := editState{caret: 4}
	if !killWordForward(&input, &s) {
		t.Fatal("killWordForward did not run")
	}
	runeEq(t, input, "word  three")
	if s.caret != 4 {
		t.Fatalf("caret = %d, want 4", s.caret)
	}
	// Caret on whitespace: the run and the following word go together;
	// from a word start, only the leading run and the word die (the run
	// between the caret and the word is the kill's whitespace part).
	input = []rune("a    next")
	s = editState{caret: 1}
	if !killWordForward(&input, &s) {
		t.Fatal("run+word kill did not run")
	}
	runeEq(t, input, "a")
	// At the end of the buffer it is a no-op.
	input = []rune("word")
	s = editState{caret: 4}
	if killWordForward(&input, &s) {
		t.Fatal("killWordForward at end of buffer must be a no-op")
	}
}

func TestMoveWordBackAndForward(t *testing.T) {
	input := []rune("one two three")
	s := editState{caret: len(input)}
	moveWordBack(input, &s)
	if s.caret != 8 {
		t.Fatalf("caret after back = %d, want 8", s.caret)
	}
	s2 := editState{caret: 8}
	moveWordBack(input, &s2)
	if s2.caret != 4 {
		t.Fatalf("caret = %d, want 4", s2.caret)
	}
	moveWordBack(input, &s2)
	if s2.caret != 0 {
		t.Fatalf("caret = %d, want 0", s2.caret)
	}
	moveWordForward(input, &s2)
	if s2.caret != 3 {
		t.Fatalf("caret after forward = %d, want 3", s2.caret)
	}
	moveWordForward(input, &s2)
	if s2.caret != 7 {
		t.Fatalf("caret = %d, want 7", s2.caret)
	}
	moveWordForward(input, &s2)
	if s2.caret != len(input) {
		t.Fatalf("caret = %d, want %d", s2.caret, len(input))
	}
}

func TestKillToStartEndAndYankRing(t *testing.T) {
	input := []rune("hello world")
	s := editState{caret: 5}
	if !killToEnd(&input, &s) {
		t.Fatal("killToEnd did not run")
	}
	runeEq(t, input, "hello")
	if !killToStart(&input, &s) {
		t.Fatal("killToStart did not run")
	}
	if len(input) != 0 {
		t.Fatalf("buffer = %q", string(input))
	}
	// Yank restores the most recent kill (killToStart's "hello").
	if !yank(&input, &s) {
		t.Fatal("yank did not run")
	}
	runeEq(t, input, "hello")
	if s.caret != 5 {
		t.Fatalf("caret = %d, want 5", s.caret)
	}
	// A repeat cycles to the next-older entry, replacing the yank in place.
	if !yank(&input, &s) {
		t.Fatal("repeat yank did not run")
	}
	runeEq(t, input, " world")
	if s.caret != 6 {
		t.Fatalf("caret = %d, want 6", s.caret)
	}
	// An edit (or buffer clear, as submit/ESC perform via endCaret) ends the
	// yank cycle.
	input = []rune("x")
	s.endCaret(input)
	insertRunes(&input, &s, []rune("y"))
	runeEq(t, input, "xy")
	if !yank(&input, &s) {
		t.Fatal("yank after edit did not run")
	}
	runeEq(t, input, "xyhello") // the cycle reset means the newest kill again
}

func TestKillRingCapsAtEight(t *testing.T) {
	s := editState{}
	for i := range 10 {
		s.pushKill([]rune{rune('a' + i)})
	}
	if len(s.ring) != killRingCap {
		t.Fatalf("ring len = %d, want %d", len(s.ring), killRingCap)
	}
	if string(s.ring[0]) != "j" { // 'a'+9, newest kept
		t.Fatalf("newest = %q, want j", string(s.ring[0]))
	}
}

func TestTranspose(t *testing.T) {
	input := []rune("abc")
	s := editState{caret: len(input)}
	if !transpose(&input, &s) {
		t.Fatal("transpose did not run")
	}
	runeEq(t, input, "acb") // end of buffer: the last two swap
	if s.caret != 3 {
		t.Fatalf("caret = %d, want 3", s.caret)
	}
	// Mid-buffer: the two runes before the caret swap, caret stays.
	input = []rune("abcd")
	s = editState{caret: 3}
	if !transpose(&input, &s) {
		t.Fatal("transpose did not run")
	}
	runeEq(t, input, "acbd")
	if s.caret != 3 {
		t.Fatalf("caret = %d, want 3", s.caret)
	}
	// Fewer than two runes before the caret: no-op.
	input = []rune("ab")
	s = editState{caret: 1}
	if transpose(&input, &s) {
		t.Fatal("transpose with <2 runes before caret must be a no-op")
	}
	runeEq(t, input, "ab")
}

func TestNewlineInsertAndFlattenOnSubmitShape(t *testing.T) {
	input := []rune("ab")
	s := editState{caret: 1}
	insertNewline(&input, &s)
	runeEq(t, input, "a\nb")
	if s.caret != 2 {
		t.Fatalf("caret = %d, want 2", s.caret)
	}
	// The submit-side flatten (the same replace the enter case performs).
	flat := strings.ReplaceAll(string(input), "\n", " ")
	runeEq(t, []rune(flat), "a b")
}
