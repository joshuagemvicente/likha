// Package highlight tokenizes fenced code for the transcript's code panels
// (spec transcript-redesign § Markdown). It only classifies text; mapping a
// Kind to theme colors is the renderer's job.
package highlight

import (
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Kind is the coarse syntax class of a Segment.
type Kind int

// Kinds, from chroma token categories.
const (
	Plain Kind = iota
	Keyword
	String
	Comment
	Number
	Name
	Function
	Type
	Operator
	Punctuation
	Builtin
	Literal
)

// Segment is a run of source text that shares one Kind.
type Segment struct {
	Text string
	Kind Kind
}

// maxCodeBytes bounds the size of one highlighted code block. Highlighting
// runs on the UI goroutine, and chroma's lexers cost about 2-5 ms per KiB on
// ordinary code, so larger blocks render plain.
const maxCodeBytes = 32 << 10

// budget bounds the time one Lines call may spend tokenizing; some lexers
// (chroma's markdown lexer among them) are superlinear on adversarial input.
// A block over budget renders plain. Tests may change it.
var budget = 100 * time.Millisecond

// Lines tokenizes code with the chroma lexer named by the fence info string
// lang. Only the first word of lang is used, case-insensitively; aliases
// such as js, ts, sh, golang, py and yml resolve through chroma's registry.
// The language is never guessed from content: an empty or unknown lang (or
// one that resolves to chroma's plaintext lexer) returns (nil, false).
//
// The result holds one []Segment per source line. Lines are split on \n,
// and a single trailing \n does not add an empty line, so "" and "\n" both
// yield one empty line. Concatenating a line's segment Texts reproduces that
// source line byte for byte (tabs, \r and any control runes included —
// callers must still sanitize before display). Adjacent segments never share
// a Kind and no segment is empty.
//
// Lines never panics. Code longer than 32 KiB, tokenizing that runs past
// the time budget (100 ms), a tokenizer error, or a token stream that does
// not reproduce the input returns (nil, false). The budget is checked after
// every raw lexer token, so it is overrun by at most one token's match time
// (chroma caps each regex match at 250 ms).
func Lines(lang, code string) (out [][]Segment, ok bool) {
	if len(code) > maxCodeBytes {
		return nil, false
	}
	lexer := lookup(lang)
	if lexer == nil {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			out, ok = nil, false
		}
	}()

	deadline := time.Now().Add(budget)
	opts := &chroma.TokeniseOptions{State: "root"} // EnsureLF off: keep \r
	// The raw lexer, not chroma.Coalesce: the coalescer reads ahead until a
	// merged token reaches 8 KiB, so a long run of same-type tokens would
	// lex unchecked between deadline checks. appendSegment merges instead.
	it, err := lexer.Tokenise(opts, code)
	if err != nil {
		return nil, false
	}

	// Lexers with EnsureNL append a newline the source may lack.
	want := code
	if !strings.HasSuffix(code, "\n") {
		want += "\n"
	}
	out = [][]Segment{nil}
	pos := 0 // bytes of want consumed
	for tok := it(); tok != chroma.EOF; tok = it() {
		if time.Now().After(deadline) {
			return nil, false
		}
		if !strings.HasPrefix(want[pos:], tok.Value) {
			return nil, false
		}
		pos += len(tok.Value)
		kind := kindOf(tok.Type)
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				out = append(out, nil)
			}
			out[len(out)-1] = appendSegment(out[len(out)-1], part, kind)
		}
	}
	if pos != len(code) && pos != len(want) {
		return nil, false
	}
	if pos > 0 && want[pos-1] == '\n' {
		out = out[:len(out)-1] // the final newline does not open a line
	}
	return out, true
}

// lookup resolves a fence info string to a lexer, or nil.
func lookup(lang string) chroma.Lexer {
	fields := strings.Fields(lang)
	if len(fields) == 0 {
		return nil
	}
	lexer := lexers.Get(strings.ToLower(fields[0]))
	if lexer == nil || lexer.Config() == nil || lexer.Config().Name == "plaintext" {
		return nil
	}
	return lexer
}

// appendSegment adds text to line, merging it into the last segment when
// the kinds match and dropping empty text.
func appendSegment(line []Segment, text string, kind Kind) []Segment {
	if text == "" {
		return line
	}
	if n := len(line); n > 0 && line[n-1].Kind == kind {
		line[n-1].Text += text
		return line
	}
	return append(line, Segment{Text: text, Kind: kind})
}

// kindOf maps a chroma token type to a Kind by category.
func kindOf(t chroma.TokenType) Kind {
	switch {
	case t == chroma.KeywordType,
		t == chroma.NameClass,
		t == chroma.NameException:
		return Type
	case t.InCategory(chroma.Keyword):
		return Keyword
	case t.InSubCategory(chroma.NameFunction), t == chroma.NameDecorator:
		return Function
	case t.InSubCategory(chroma.NameBuiltin):
		return Builtin
	case t.InCategory(chroma.Name):
		return Name
	case t.InSubCategory(chroma.LiteralString):
		return String
	case t.InSubCategory(chroma.LiteralNumber):
		return Number
	case t.InCategory(chroma.Literal):
		return Literal
	case t.InCategory(chroma.Operator):
		return Operator
	case t == chroma.Punctuation, t == chroma.TextPunctuation:
		return Punctuation
	case t.InCategory(chroma.Comment):
		return Comment
	}
	return Plain
}
