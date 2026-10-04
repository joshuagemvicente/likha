package tui

import (
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"likha/internal/model"
	"likha/internal/providers"
	likhaui "likha/internal/ui"
)

// statusSegment is measured as plain terminal cells before any theme styling.
// Rendering only after fitting prevents escape sequences from being clipped.
type statusSegment struct {
	text  string
	style lipgloss.Style
}

func (m *ui) statusLineHeight() int {
	if m.width < 70 {
		return 2
	}
	return 1
}

func (m *ui) refreshStatusSessionTitle() {
	if !m.statusLineOpts.Session {
		return
	}
	snapshot := m.snapshot
	// The first prompt is known before a turn completes (and even when there
	// is no session store), so use the current history for the live title.
	snapshot.History = m.history
	m.statusTitle = statusSessionTitle(snapshot)
}

// statusState marks the status state with the opted-in Nerd Font marker
// (FR-15, themes spec): icons appear only as status and review markers and
// only under --nerd-fonts; plain output stays identical without the flag.
func (m *ui) statusState() string {
	state := m.status
	if !m.conn.Nerd || m.pending != nil {
		return state
	}
	switch {
	case state == "Waiting for model" || state == "Compacting…":
		return m.glyphs.Waiting + " " + state
	case state == "Reading repository" || strings.HasPrefix(state, "Executing approved "):
		return m.glyphs.Working + " " + state
	case state == "Ready":
		return m.glyphs.Done + " " + state
	case state == "Error" || state == "Not connected" || state == "Session save failed":
		return m.glyphs.Error + " " + state
	case state == "Resumed session":
		return m.glyphs.Resume + " " + state
	}
	return state
}

// reviewKind prefixes a review label with the pencil marker under the Nerd
// Font opt-in; plain terminals keep the bare label.
func (m *ui) reviewMark(kind string) string {
	if !m.conn.Nerd {
		return kind
	}
	return m.glyphs.Review + " " + kind
}

func (m *ui) statusHints(page, pages int, narrow bool) []string {
	position := fmt.Sprintf("Page %d/%d", page, pages)
	if m.pending != nil {
		controls := "←/→ · Enter · Esc"
		// The fallback row stays compact: the wider form pushes the model
		// identity into a trim at 80 columns (FR-12 keeps it visible).
		compact := "←/→ Enter Esc"
		if narrow {
			controls = compact
		}
		if m.status == reviewGateStatus {
			return []string{"Read all pages " + position + " " + controls, "Review " + position + " " + compact}
		}
		kind := m.reviewMark("Review")
		if m.pending.Kind == "command" {
			kind = m.reviewMark("Command review")
		} else if m.pending.Kind == "mcp" {
			kind = m.reviewMark("MCP review")
		}
		return []string{kind + " · " + position + " · " + controls, "Review " + position + " " + compact}
	}
	state := m.statusState()
	if _, ok := m.focusedThoughtEntry(); ok {
		if m.working {
			return []string{m.toolInspectionHelp() + " · Ctrl+C cancel", position + " Tab · Enter expand · ^C cancel", "Tab · Enter · ^C cancel"}
		}
		return []string{m.toolInspectionHelp(), position + " Tab · Enter expand · Back unfocus", "Tab · Enter · Back unfocus"}
	}
	if m.toolInspector.focused {
		if m.working {
			return []string{m.toolInspectionHelp() + " · Ctrl+C cancel", position + " Tab · ^O inspect · ^C cancel", "Tab · ^O inspect · ^C cancel"}
		}
		return []string{m.toolInspectionHelp(), position + " Tab · ^O inspect · Back unfocus", "Tab · ^O inspect · Back unfocus"}
	}
	if narrow {
		if m.working {
			queued := ""
			if n := len(m.queue); n > 0 {
				queued = fmt.Sprintf("%dQ ", n)
			}
			return []string{state + " " + queued + position + " PgUp/PgDn ^C", position + " PgUp/PgDn ^C"}
		}
		held := ""
		if n := len(m.queue); n > 0 {
			held = fmt.Sprintf("%dQ ", n)
		}
		return []string{state + " " + held + position + " PgUp/PgDn Enter ^G", position + " PgUp/PgDn Enter ^G"}
	}
	if m.working {
		queued := ""
		if n := len(m.queue); n > 0 {
			queued = fmt.Sprintf("%d queued", n) + " · "
		}
		return []string{state + " · " + queued + position + " · PgUp/PgDn · Ctrl+C cancel", state + " · " + queued + position + " · PgUp/PgDn ^C", position + " · PgUp/PgDn ^C"}
	}
	held := ""
	if n := len(m.queue); n > 0 {
		held = fmt.Sprintf("%d queued", n) + " · "
	}
	hints := []string{state + " · " + held + position + " · Enter send · PgUp/PgDn · Ctrl+G composer", state + " · " + position + " · PgUp/PgDn Enter ^G", position + " · PgUp/PgDn Enter", position + " · PgUp/PgDn"}
	if len(m.toolRecords) > 0 {
		return []string{state + " · " + held + position + " · Tab tool · Ctrl+O inspect · Enter send", position + " Tab tool · ^O inspect · Enter", position + " ^O inspect · Enter"}
	}
	return hints
}

// Keep control characters out of measurements and render only plain text.
func statusField(text string, width int) string {
	return strings.TrimRight(fit(text, max(1, width)), " ")
}

// contextSegment shows the active conversation's input-token count and, when
// known, the context window. Estimates remain visibly approximate; counts and
// limits are retained on narrow rows while the percentage is omitted.
func (m *ui) contextSegment() statusSegment {
	if !m.contextSeen || m.contextTokens < 0 {
		return statusSegment{"ctx —", m.theme.Muted}
	}

	approximation := ""
	if m.contextEstimated {
		approximation = "~"
	}
	text := "ctx " + approximation + humanTokens(m.contextTokens)
	style := m.theme.Normal
	if !m.contextWindowKnown || m.contextWindow <= 0 {
		return statusSegment{text + "/?", style}
	}

	text += "/" + humanTokens(m.contextWindow)
	pct := int(math.Round(float64(m.contextTokens) / float64(m.contextWindow) * 100))
	if pct >= 80 {
		style = m.theme.Warning
	}
	if m.statusLineHeight() == 1 {
		text += " · " + approximation + strconv.Itoa(pct) + "%"
	}
	return statusSegment{text, style}
}

// humanTokens formats a token count compactly: raw under 1000, thousands
// with a decimal only when one is needed ("68k", "68.4k"), and millions as
// "1M"/"1.5M". Values rounding onto the next unit render at that unit, so
// 999,999 reads "1M".
func humanTokens(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 995_000:
		return compactUnit(float64(n)/1000, "k")
	default:
		return compactUnit(float64(n)/1_000_000, "M")
	}
}

// compactUnit renders a value with its unit suffix, one decimal only when
// the value is small and not whole.
func compactUnit(value float64, unit string) string {
	if value < 100 && math.Abs(value-math.Round(value)) > 0.049 {
		return fmt.Sprintf("%.1f%s", value, unit)
	}
	return strconv.FormatInt(int64(math.Round(value)), 10) + unit
}

// statusCountSegment renders a dirty or staged count; zero hides the segment.
func statusCountSegment(count int, label string) string {
	if count <= 0 {
		return ""
	}
	return fmt.Sprintf("%d %s", count, label)
}

func (m *ui) statusOptional() []statusSegment {
	var segments []statusSegment
	add := func(enabled bool, value string) {
		if enabled && value != "" {
			segments = append(segments, statusSegment{statusField(value, m.width), m.theme.Muted})
		}
	}
	add(providers.FlagEnabled(m.statusLineOpts.Folder), m.statusFolder)
	// Git segments (spec tui-layout 1b.4): one bounded git status read feeds
	// every segment, and a failed read (git missing, timeout, non-repository)
	// hides them all rather than rendering stale zeros.
	if m.gitOK {
		// An unborn branch renders "No commits yet on <name>"; show the name.
		branch := strings.TrimPrefix(m.git.Branch, "No commits yet on ")
		add(providers.FlagEnabled(m.statusLineOpts.Branch), branch)
		add(providers.FlagEnabled(m.statusLineOpts.Branch), aheadBehindSegment(m.git))
		add(m.statusLineOpts.Staged, statusCountSegment(m.git.Staged, "staged"))
		add(m.statusLineOpts.Changes, statusCountSegment(m.git.Dirty, "changed"))
		add(m.statusLineOpts.Changes, statusCountSegment(m.git.Untracked, "untracked"))
	}
	add(m.statusLineOpts.MCP, m.conn.Mcp.Summary())
	add(m.statusLineOpts.Session, m.statusTitle)
	add(true, envSegment())
	add(m.statusLineOpts.Minutes, fmt.Sprintf("minutes %dm", int(time.Since(m.started).Minutes())))
	add(m.statusLineOpts.Tokens, m.tokensSegment())
	add(true, m.spendSegment())
	add(m.statusLineOpts.Version, "v"+strings.TrimPrefix(Version, "v"))
	add(m.statusLineOpts.Update, m.updateSegment())
	return segments
}

// aheadBehindSegment renders the upstream delta as compact arrows, omitting
// the zero side: "↓1", "↑2", "↓1↑2"; empty when in sync.
func aheadBehindSegment(state gitState) string {
	var b strings.Builder
	if state.Behind > 0 {
		fmt.Fprintf(&b, "↓%d", state.Behind)
	}
	if state.Ahead > 0 {
		fmt.Fprintf(&b, "↑%d", state.Ahead)
	}
	return b.String()
}

// envSegment names the runtime platform — "macOS arm64", "Linux amd64" —
// from the Go runtime. It has no config gate; width gating happens with the
// other optional segments (spec tui-layout 1b.5).
func envSegment() string {
	return envLabel(runtime.GOOS) + " " + runtime.GOARCH
}

// envLabel maps GOOS onto its display label; anything unmapped passes
// through raw.
func envLabel(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	}
	return goos
}

// spendSegment renders the accumulated session cost once a turn carried
// usable pricing: two decimals always ("$0.42"; a subscription row is the
// known zero "$0.00"). A model without documented pricing never fabricates
// an amount (spec tui-layout 1b.6).
func (m *ui) spendSegment() string {
	if !m.spendKnown {
		return ""
	}
	return fmt.Sprintf("$%.2f", m.spend)
}

// tokensSegment reports raw prompt+completion counts, secondary to the ctx %.
func (m *ui) tokensSegment() string {
	if !m.usageSeen {
		return ""
	}
	return fmt.Sprintf("tokens %d", m.usagePrompt+m.usageCompletion)
}

// updateSegment renders the update-available notice for a known newer
// release; an empty version means nothing to show.
func (m *ui) updateSegment() string {
	if m.updateVersion == "" {
		return ""
	}
	return "update → v" + strings.TrimPrefix(m.updateVersion, "v")
}

func statusWidth(segments []statusSegment) int {
	width := 0
	for i, segment := range segments {
		if i > 0 {
			width += 3 // plain ' · ' separator
		}
		width += runewidth.StringWidth(segment.text)
	}
	return width
}

// statusIdentity retains both the active provider and model, abbreviating a
// provider's parenthetical plan before shortening the model on tight screens.
// The model renders its curated display name where one exists, falling back
// to the slug (spec tui-layout 1b.2).
func (m *ui) statusIdentity(maxWidth int, usage string) []statusSegment {
	provider := statusField(m.conn.Provider, m.width)
	if provider == "" {
		provider = "Provider"
	} else if !m.conn.Verified {
		// v1 spec §3: an endpoint outside the accepted list runs with a
		// visible unverified warning. The identity row is the provider
		// display; the tight-width abbreviation below may drop the marker
		// before it ever clips page controls.
		provider += " (unverified)"
	}
	modelName := statusField(model.ModelDisplayName(m.modelName), m.width)
	if modelName == "" {
		modelName = "—"
	}
	usage = statusField(usage, m.width)
	ctx := m.contextSegment()
	makeParts := func(p, name string) []statusSegment {
		// Plan mode is an unmissable persistent marker on every frame: it
		// precedes the identity so trimming the model retires before it.
		marker := []statusSegment{}
		if m.planMode {
			marker = []statusSegment{{"PLAN MODE", m.theme.Selected}}
		}
		parts := append(marker, []statusSegment{{p, m.theme.Selected}, {name, m.theme.Normal}, ctx}...)
		if usage != "" {
			parts = append(parts, statusSegment{usage, m.theme.Help})
		}
		return parts
	}
	parts := makeParts(provider, modelName)
	if statusWidth(parts) <= maxWidth {
		return parts
	}
	if n := strings.Index(provider, " ("); n > 0 {
		provider = provider[:n]
		parts = makeParts(provider, modelName)
		if statusWidth(parts) <= maxWidth {
			return parts
		}
	}
	// Preserve the visible provider and ctx placeholder; trim only the model
	// before clipping critical page and mode controls.
	room := maxWidth - statusWidth(makeParts(provider, ""))
	if room < 1 {
		provider = strings.TrimSpace(fit(provider, max(1, runewidth.StringWidth(provider)+room-1)))
		room = maxWidth - statusWidth(makeParts(provider, ""))
	}
	if room < runewidth.StringWidth(modelName) {
		modelName = strings.TrimSpace(fit(modelName, max(1, room)))
	}
	return makeParts(provider, modelName)
}

func (m *ui) statusLeft(maxWidth int) []statusSegment {
	usage := ""
	if m.client != nil {
		usage = m.client.Usage()
	}
	parts := m.statusIdentity(maxWidth, "")
	if usage != "" {
		// A reported rate window rides along whenever its identity variant
		// fits; statusIdentity already trims the model to make room.
		if withUsage := m.statusIdentity(maxWidth, usage); statusWidth(withUsage) <= maxWidth {
			parts = withUsage
		}
	}
	for _, optional := range m.statusOptional() {
		if statusWidth(parts)+3+runewidth.StringWidth(optional.text) <= maxWidth {
			parts = append(parts, optional)
		}
	}
	return parts
}

// statusMarkWidth is the cells the bottom-right Likha mark claims, including
// its two-space gap; zero below the wordmark's 70-column threshold.
func (m *ui) statusMarkWidth() int {
	if m.width >= 70 {
		return runewidth.StringWidth("Likha") + 2
	}
	return 0
}
func (m *ui) renderStatusRow(left []statusSegment, right string, warning bool) string {
	return m.renderStatusCells(left, right, warning)
}

// renderStatusCanvas paints a fully-laid-out status row onto the theme
// canvas. mainView is the only caller: tests keep calling renderStatusRow
// and statusLineRows for plain measurable rows.
func (m *ui) renderStatusCanvas(row string) string {
	// Pad to full width first (plain spaces), then paint the canvas behind
	// the whole row: role colors survive because PaintRow re-applies the bg
	// around each SGR span instead of re-styling text.
	return likhaui.PaintRow(row, m.width, m.theme.BaseBG())
}

func (m *ui) renderStatusCells(left []statusSegment, right string, warning bool) string {
	var b strings.Builder
	for i, part := range left {
		if i > 0 {
			b.WriteString(m.theme.Muted.Render(" · "))
		}
		b.WriteString(part.style.Render(part.text))
	}
	rightStyle := m.theme.Help
	if warning {
		rightStyle = m.theme.Warning
	}
	// The Likha mark lives at the far right END of the row, after the page or
	// mode hint (spec tui-layout 1a.3). It renders only at 70 columns and up
	// and retires first under width pressure: callers already reserve its
	// cells before hint downgrades and optional segments, and here it yields
	// rather than clipping the hint.
	if markWidth := m.statusMarkWidth(); markWidth > 0 {
		space := m.width - statusWidth(left) - runewidth.StringWidth(right) - markWidth
		if space > 0 {
			b.WriteString(strings.Repeat(" ", space))
			if right != "" {
				b.WriteString(rightStyle.Render(right))
			}
			b.WriteString(strings.Repeat(" ", 2))
			b.WriteString(m.theme.Title.Render("Likha"))
			return b.String()
		}
	}
	space := m.width - statusWidth(left) - runewidth.StringWidth(right)
	if space > 0 {
		b.WriteString(strings.Repeat(" ", space))
	}
	if right != "" {
		b.WriteString(rightStyle.Render(right))
	}
	return b.String()
}

// statusLineRows fits content as plain text, then applies theme roles. Its row
// count depends only on width so changing mode or optional values cannot move
// review page boundaries.
func (m *ui) statusLineRows(page, pages int) []string {
	narrow := m.statusLineHeight() == 2
	hints := m.statusHints(page, pages, narrow)
	if narrow {
		left := m.statusLeft(m.width)
		right := hints[0]
		for _, hint := range hints {
			if runewidth.StringWidth(hint) <= m.width {
				right = hint
				break
			}
		}
		return []string{m.renderStatusRow(left, "", false), m.renderStatusRow(nil, fitHint(right, m.width), m.pending != nil)}
	}
	// Reserve the right-hand controls first, then the Likha mark's cells: the
	// mark retires before any hint downgrade and before optional segments
	// retire, so every width computation below happens inside the
	// mark-reserving budget. A rate-window summary is retained before
	// optional decorations whenever a compact mode hint makes it fit.
	usage := ""
	if m.client != nil {
		usage = m.client.Usage()
	}
	markWidth := m.statusMarkWidth()
	right := hints[len(hints)-1]
	if usage != "" {
		// A rate window outranks optional decorations: trade down through the
		// hints (most informative first) until the usage-bearing identity —
		// which may trim its model — fits beside the hint and the mark.
		for _, hint := range hints {
			available := m.width - runewidth.StringWidth(hint) - 3 - markWidth
			if statusWidth(m.statusIdentity(available, usage)) <= available {
				right = hint
				break
			}
		}
	} else {
		// Without a rate window, retain the most informative hint that fits
		// the full identity — the state text (Waiting for model, Cancelling,
		// Ready) is the footer's status string and must not be pinned to the
		// shortest variant just because optional segments share the row.
		for _, hint := range hints {
			available := m.width - runewidth.StringWidth(hint) - 3 - markWidth
			if available >= statusWidth(m.statusIdentity(m.width, "")) {
				right = hint
				break
			}
		}
	}
	available := m.width - runewidth.StringWidth(right) - 3 - markWidth
	left := m.statusLeft(available)
	return []string{m.renderStatusRow(left, right, m.pending != nil)}
}

func fitHint(text string, width int) string {
	if runewidth.StringWidth(text) > width {
		return strings.TrimRight(fit(text, width), " ")
	}
	return text
}
