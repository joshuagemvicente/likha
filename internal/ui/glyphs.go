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
