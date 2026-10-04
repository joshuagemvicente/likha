package ui

// Glyphs holds the marker set for status and review lines. Ascii carries the
// plain-text fallback used when the user has not opted into Nerd Fonts, so
// output stays legible everywhere.
type Glyphs struct {
	Waiting string // waiting for the model
	Working string // executing a tool or action
	Done    string // completed turn
	Error   string // failed or rejected
	Review  string // pending review marker
	Resume  string // resumed session
}

// AsciiGlyphs is the always-safe marker set.
func AsciiGlyphs() Glyphs {
	return Glyphs{Waiting: "·", Working: "»", Done: "=", Error: "!", Review: "!", Resume: "~"}
}

// NerdGlyphs is the marker set for terminals with a patched Nerd Font. Used
// only when the user opts in explicitly (nerd codepoints: clock, gear, check,
// cross, pencil, history).
func NerdGlyphs() Glyphs {
	return Glyphs{
		Waiting: "\U000f0787", // nf-oct-clock
		Working: "\U000f04a2", // nf-fa-gear / nf-md-settings
		Done:    "\U000f012c", // nf-oct-check / nf-fa-check
		Error:   "\U000f0156", // nf-oct-x
		Review:  "\U000f09eb", // nf-oct-pencil
		Resume:  "\U000f0521", // nf-fa-history
	}
}

// BlockGlyphs is the transcript block glyph language: one marker per block
// kind in a two-cell gutter, plus the truncation mark and the rounded
// composer box. Unicode is the default; Ascii carries the plain fallback
// selected by --ascii / LIKHA_ASCII=1. Nerd Font icons never replace these.
// Every glyph except Ellipsis is one display cell in both sets.
type BlockGlyphs struct {
	User       string // user prompt
	Assistant  string // assistant text
	ToolHeader string // tool call header (dot colored by status)
	Result     string // tool result / sub-line
	Error      string // error block
	Notice     string // Likha notice
	Thought    string // reasoning marker and turn footer
	Focus      string // focused tool item gutter
	Ellipsis   string // truncation mark (three cells in ASCII)

	// Composer box: corners and edges.
	TopLeft     string
	TopRight    string
	BottomLeft  string
	BottomRight string
	Horizontal  string
	Vertical    string
}

// UnicodeBlockGlyphs is the default block glyph set.
func UnicodeBlockGlyphs() BlockGlyphs {
	return BlockGlyphs{
		User: ">", Assistant: "⏺", ToolHeader: "⏺", Result: "⎿",
		Error: "✗", Notice: "ℹ", Thought: "✻", Focus: "›", Ellipsis: "…",
		TopLeft: "╭", TopRight: "╮", BottomLeft: "╰", BottomRight: "╯",
		Horizontal: "─", Vertical: "│",
	}
}

// AsciiBlockGlyphs is the plain-ASCII block glyph set for terminals or fonts
// without the Unicode block marks.
func AsciiBlockGlyphs() BlockGlyphs {
	return BlockGlyphs{
		User: ">", Assistant: "*", ToolHeader: "*", Result: "L",
		Error: "x", Notice: "i", Thought: "~", Focus: ">", Ellipsis: "...",
		TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
		Horizontal: "-", Vertical: "|",
	}
}

// BlockGlyphSet selects the ASCII set when ascii is true, else Unicode.
func BlockGlyphSet(ascii bool) BlockGlyphs {
	if ascii {
		return AsciiBlockGlyphs()
	}
	return UnicodeBlockGlyphs()
}
