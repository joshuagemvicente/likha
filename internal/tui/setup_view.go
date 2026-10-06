package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"likha/internal/model"
	likhaui "likha/internal/ui"
)

// First-run onboarding view (specs/onboarding-redesign): a full-screen page
// with the brand, a four-step indicator, the stage heading and body, and a
// key-hint footer pinned to the last row. Rows are assembled from plain
// spans that are clipped first and styled once, so a style never wraps an
// escape sequence and no row is re-fitted after styling. Every row paints
// the theme canvas edge to edge, so the live theme preview restyles the
// whole page.

// span is one styled run of a setup row. Text is plain and may be untrusted
// (model IDs, error text): clipping escapes hidden runes before styling.
type span struct {
	text  string
	style lipgloss.Style
}

// setupLayout is the content column of the setup page.
type setupLayout struct {
	left, col int  // left margin and content column width
	logo      bool // the ASCII logo fits; otherwise a one-row wordmark
	compact   bool // short terminal: no top padding, wordmark, or subtitle
}

// setupMaxCol bounds the content column so lists stay scannable on wide
// terminals; the column centers in the remaining width.
const setupMaxCol = 76

func (m *ui) setupLayout() setupLayout {
	col := max(1, min(m.width-4, setupMaxCol))
	return setupLayout{
		col:     col,
		left:    max(2, (m.width-col)/2),
		logo:    m.height >= 30 && col >= logoWidth(),
		compact: m.height < 20,
	}
}

func logoWidth() int {
	w := 0
	for _, line := range strings.Split(logo, "\n") {
		w = max(w, runewidth.StringWidth(line))
	}
	return w
}

func (m *ui) setupView() string {
	l := m.setupLayout()
	head := m.setupHead(l)
	body := m.setupBody(l, max(0, m.height-len(head)-2))
	rows := append(head, body...)
	if limit := max(0, m.height-2); len(rows) > limit {
		rows = rows[:limit]
	}
	for len(rows) < m.height-1 {
		rows = append(rows, m.setupRow(l))
	}
	rows = append(rows, m.setupFooter(l))
	return strings.Join(rows[:m.height], "\n")
}

// setupHead is everything above the stage body: brand, steps, heading.
func (m *ui) setupHead(l setupLayout) []string {
	var rows []string
	if !l.compact {
		rows = append(rows, m.setupRow(l))
		if l.logo {
			for _, line := range strings.Split(logo, "\n") {
				rows = append(rows, m.setupRow(l, span{line, m.theme.Title}))
			}
			rows = append(rows, m.setupRow(l, span{"Welcome to Likha. Let's connect a model.", m.theme.Muted}))
		} else {
			rows = append(rows, m.setupRow(l, span{"Likha", m.theme.Title}, span{"  ·  first-run setup", m.theme.Muted}))
		}
		rows = append(rows, m.setupRow(l))
	}
	rows = append(rows, m.setupRow(l, m.setupStepper(l.col)...))
	rows = append(rows, m.setupRow(l))
	title, sub := m.setupHeading()
	rows = append(rows, m.setupRow(l, span{title, m.theme.Title}))
	if !l.compact && sub != "" {
		for _, line := range wrapWords(sub, l.col) {
			rows = append(rows, m.setupRow(l, span{line, m.theme.Muted}))
		}
	}
	rows = append(rows, m.setupRow(l))
	return rows
}

// setupStep maps the stage onto the four visible steps.
func (m *ui) setupStep() int {
	switch m.setup.stage {
	case setupProvider:
		return 0
	case setupKey, setupChecking, setupLogin, setupPlanNotice:
		return 1
	case setupModel:
		return 2
	}
	return 3
}

// setupStepper renders "✓ Provider ── ● Connect ── ○ Model ── ○ Theme", or
// "Step 2 of 4 · Connect" when that does not fit. Each step carries a glyph
// and a word, so progress never depends on color.
func (m *ui) setupStepper(width int) []span {
	labels := []string{"Provider", "Connect", "Model", "Theme"}
	if model.Providers[m.setup.cursor].Auth == model.AuthOAuth {
		labels[1] = "Sign in"
	}
	done, current, todo, join := "✓", "●", "○", " ── "
	if m.conn.ASCII {
		done, current, todo, join = "+", "*", "-", " -- "
	}
	step := m.setupStep()
	var spans []span
	for i, label := range labels {
		if i > 0 {
			spans = append(spans, span{join, m.theme.Muted})
		}
		switch {
		case i < step:
			spans = append(spans, span{done + " ", m.theme.Success}, span{label, m.theme.Muted})
		case i == step:
			spans = append(spans, span{current + " " + label, m.theme.Title})
		default:
			spans = append(spans, span{todo + " " + label, m.theme.Muted})
		}
	}
	if spansWidth(spans) <= width {
		return spans
	}
	return []span{
		{"Step " + strconv.Itoa(step+1) + " of " + strconv.Itoa(len(labels)), m.theme.Title},
		{"  ·  " + labels[step], m.theme.Muted},
	}
}

// setupHeading is the stage title and its one-paragraph explanation.
func (m *ui) setupHeading() (title, sub string) {
	p := model.Providers[m.setup.cursor]
	switch m.setup.stage {
	case setupProvider:
		return "Choose a provider", "Likha hosts no models: it talks to your provider with your own account. You can add more providers later with /providers."
	case setupKey, setupChecking:
		sub = "Paste an API key for " + p.DisplayName + ". Likha checks it with the provider, then keeps it only in its private state directory (mode 0600)."
		if setupEnvKeySet(p) {
			sub = p.KeyEnv + " is set, so Enter uses that key and nothing is written to disk. Or paste a different key."
		}
		return "Connect " + p.DisplayName, sub
	case setupLogin:
		return "Sign in to " + strings.TrimSuffix(p.DisplayName, " (Plus/Pro)"), "Likha automatically opens your browser to sign in and authorize your shared ChatGPT plan allowance. No API key is needed."
	case setupPlanNotice:
		return chatGPTPlanTitle, ""
	case setupModel:
		if p.Auth == model.AuthOAuth {
			return "Choose a model", "Models available to this ChatGPT account, reported by OpenAI. Usage shares your plan allowance and limits."
		}
		n := len(m.setup.models)
		noun := "models"
		if n == 1 {
			noun = "model"
		}
		return "Choose a model", p.DisplayName + " reports " + strconv.Itoa(n) + " " + noun + ". Switch anytime with /models."
	}
	return "Pick a theme", "The whole screen previews each theme as you move. Change it anytime with /themes."
}

// setupBody renders the stage body into at most avail rows.
func (m *ui) setupBody(l setupLayout, avail int) []string {
	switch m.setup.stage {
	case setupProvider:
		return m.setupProviderBody(l, avail)
	case setupKey, setupChecking:
		return m.setupKeyBody(l)
	case setupLogin:
		return m.setupLoginBody(l)
	case setupPlanNotice:
		rows := m.setupWrapped(l, span{}, chatGPTPlanBody, m.theme.Normal)
		rows = append(rows, m.setupRow(l))
		rows = append(rows, m.setupWrapped(l, span{}, "Usage: "+chatGPTUsageURL, m.theme.Muted)...)
		rows = append(rows, m.setupRow(l), m.setupRow(l, span{"Got it · Enter", m.theme.Accent}))
		return m.appendSetupError(l, rows)
	case setupModel:
		return m.setupModelBody(l, avail)
	}
	return m.setupThemeBody(l, avail)
}

// setupListRows is the list capacity of the current stage: the body height
// minus the filter row and its spacer. PgUp/PgDn page by it.
func (m *ui) setupListRows() int {
	l := m.setupLayout()
	avail := m.height - len(m.setupHead(l)) - 2
	if m.setup.stage == setupTheme {
		return avail
	}
	return avail - 2
}

func (m *ui) setupProviderBody(l setupLayout, avail int) []string {
	matches := setupProviderMatches(string(m.setup.filter))
	rows := []string{m.setupFilterRow(l, matches, m.setup.cursor, "providers"), m.setupRow(l)}
	if len(matches) == 0 {
		return append(rows, m.setupRow(l, span{"No providers match \"" + string(m.setup.filter) + "\"", m.theme.Muted}))
	}
	pos := indexOf(matches, m.setup.cursor)
	count, start := windowList(len(matches), max(0, pos), max(1, avail-len(rows)))
	for _, idx := range matches[start : start+count] {
		p := model.Providers[idx]
		var right span
		switch {
		case p.Auth == model.AuthOAuth:
			right = span{"browser sign-in", m.theme.Accent}
		case setupEnvKeySet(p):
			right = span{"key found in " + p.KeyEnv, m.theme.Success}
		default:
			right = span{providerHost(p.BaseURL), m.theme.Muted}
		}
		rows = append(rows, m.setupRow(l, m.setupItem(l.col, idx == m.setup.cursor, p.DisplayName, right)...))
	}
	return rows
}

func (m *ui) setupModelBody(l setupLayout, avail int) []string {
	matches := m.setupModelMatches()
	rows := []string{m.setupFilterRow(l, matches, m.setup.modelCursor, "models"), m.setupRow(l)}
	if len(matches) == 0 {
		rows = append(rows, m.setupRow(l, span{"No models match \"" + string(m.setup.filter) + "\"", m.theme.Muted}))
		return m.appendSetupError(l, rows)
	}
	provider := model.Providers[m.setup.cursor]
	def := provider.DefaultModel
	pos := indexOf(matches, m.setup.modelCursor)
	count, start := windowList(len(matches), max(0, pos), max(1, avail-len(rows)-m.setupErrorRows(l)))
	for _, idx := range matches[start : start+count] {
		id := m.setup.models[idx]
		// Details, front to back: context (live window, else the bundled
		// catalog's exact pair), price per 1M tokens, recommended.
		// setupItem drops them from the front on narrow rows.
		var right []span
		detail := func(text string, style lipgloss.Style) {
			if len(right) > 0 {
				right = append(right, span{"  ", m.theme.Muted})
			}
			right = append(right, span{text, style})
		}
		if w, ok := model.ResolveContextWindow(provider.Name, id, 0, m.setup.contextWindows[id]); ok {
			detail(humanTokens(w)+" context", m.theme.Muted)
		}
		if price := modelPriceLabel(provider.Name, id); price != "" {
			detail(price, m.theme.Muted)
		}
		if id == def {
			detail("recommended", m.theme.Accent)
		}
		label := id
		if name := m.setup.modelNames[id]; name != "" && name != id {
			label = name + " (" + id + ")"
		}
		rows = append(rows, m.setupRow(l, m.setupItem(l.col, idx == m.setup.modelCursor, label, right...)...))
	}
	return m.appendSetupError(l, rows)
}

// setupFilterRow shows the type-to-filter field and the cursor position.
func (m *ui) setupFilterRow(l setupLayout, matches []int, cursor int, noun string) string {
	left := []span{{"/ ", m.theme.Accent}}
	if len(m.setup.filter) == 0 {
		left = append(left, span{"type to filter " + noun, m.theme.Muted})
	} else {
		left = append(left, span{string(m.setup.filter), m.theme.Normal}, span{m.caretGlyph(), m.theme.Accent})
	}
	count := strconv.Itoa(len(matches)) + " " + noun
	if pos := indexOf(matches, cursor); pos >= 0 {
		count = strconv.Itoa(pos+1) + " of " + strconv.Itoa(len(matches))
	}
	return m.setupRow(l, justify(l.col, left, []span{{count, m.theme.Muted}})...)
}

// setupKeyBody is the framed, masked key field plus the check status.
func (m *ui) setupKeyBody(l setupLayout) []string {
	p := model.Providers[m.setup.cursor]
	checking := m.setup.stage == setupChecking && m.setup.checking
	border := m.theme.Accent
	if checking {
		border = m.theme.Muted
	}
	inner := max(1, l.col-4)
	bullet := "•"
	if m.conn.ASCII {
		bullet = "*"
	}
	var field []span
	n := len(m.setup.keyInput)
	switch {
	case n == 0 && setupEnvKeySet(p):
		field = []span{{m.caretGlyph(), m.theme.Accent}, {"press Enter to use $" + p.KeyEnv, m.theme.Muted}}
	case n == 0:
		field = []span{{m.caretGlyph(), m.theme.Accent}, {"paste your API key", m.theme.Muted}}
	default:
		masked := strings.Repeat(bullet, n)
		if room := inner - 1; n > room {
			ell := m.blocks.Ellipsis
			masked = ell + strings.Repeat(bullet, max(0, room-runewidth.StringWidth(ell)))
		}
		field = []span{{masked, m.theme.Normal}}
		if !checking {
			field = append(field, span{m.caretGlyph(), m.theme.Accent})
		}
	}
	var rows []string
	for _, spans := range m.setupBoxSpans(l.col, "API key", border, []setupBoxLine{{spans: field}}) {
		rows = append(rows, m.setupRow(l, spans...))
	}
	if !l.compact {
		// The count confirms a paste landed without revealing the key.
		count := ""
		if n > 0 {
			count = "  " + strconv.Itoa(n) + " characters"
		}
		rows = append(rows, m.setupRow(l, span{count, m.theme.Muted}))
	}
	switch {
	case checking:
		if !l.compact {
			rows = append(rows, m.setupRow(l))
		}
		rows = append(rows, m.setupWrapped(l, span{activitySpinner(m.setup.spin) + " ", m.theme.Accent},
			"Checking the key with "+providerHost(p.BaseURL)+"…", m.theme.Normal)...)
	case m.setup.err != "":
		// The error outranks every hint: on short terminals it is the
		// only thing under the field.
		rows = m.appendSetupError(l, rows)
		if !l.compact {
			rows = append(rows, m.setupRow(l))
			rows = append(rows, m.setupWrapped(l, span{}, "Fix the key and press Enter to try again, or Esc to pick another provider.", m.theme.Muted)...)
		}
	case p.KeyEnv != "" && !setupEnvKeySet(p) && !l.compact:
		rows = append(rows, m.setupRow(l))
		rows = append(rows, m.setupWrapped(l, span{}, "Tip: export "+p.KeyEnv+" to use a key from your environment instead of storing one.", m.theme.Muted)...)
	}
	return rows
}

// setupLoginBody walks through the browser sign-in and shows its progress.
func (m *ui) setupLoginBody(l setupLayout) []string {
	steps := []string{
		"Your browser opens automatically",
		"Sign in and authorize shared ChatGPT plan access",
		"Setup continues automatically after authorization",
	}
	var rows []string
	if l.compact && (m.setup.checking || m.setup.err != "") {
		// Short terminal: the wait or the failure is what matters now.
		steps = nil
	}
	for i, step := range steps {
		style := m.theme.Normal
		if m.setup.checking && i == 0 {
			style = m.theme.Muted
		}
		rows = append(rows, m.setupWrapped(l, span{"  " + strconv.Itoa(i+1) + "  ", m.theme.Accent}, step, style)...)
	}
	switch {
	case m.setup.checking:
		if len(rows) > 0 {
			rows = append(rows, m.setupRow(l))
		}
		progress := m.oauth.progress
		if progress == "" {
			progress = "Opening your browser…"
		}
		rows = append(rows, m.setupWrapped(l, span{activitySpinner(m.setup.spin) + " ", m.theme.Accent}, progress, m.theme.Normal)...)
	case m.setup.err != "":
		rows = m.appendSetupError(l, rows)
	}
	if m.oauth.manualURL != "" {
		rows = append(rows, m.setupWrapped(l, span{}, m.oauth.manualURL, m.theme.Normal)...)
	}
	return rows
}

// setupThemeBody lists the themes beside a sample transcript painted in the
// highlighted theme. Narrow columns drop the preview.
func (m *ui) setupThemeBody(l setupLayout, avail int) []string {
	names := likhaui.ThemeNames()
	listW := 2
	for _, name := range names {
		listW = max(listW, runewidth.StringWidth(name)+6)
	}
	preview := m.setupThemeSample()
	count, start := windowList(len(names), m.setup.themeCursor, max(1, avail))
	var boxSpans [][]span
	if l.col-listW-2 >= 34 && avail >= len(preview)+2 {
		boxSpans = m.setupBoxSpans(l.col-listW-2, "Preview", m.theme.Border, preview)
	}
	var rows []string
	for i := 0; i < max(count, len(boxSpans)) && i < avail; i++ {
		var spans []span
		if idx := start + i; i < count && idx < len(names) {
			spans = m.setupItem(listW, idx == m.setup.themeCursor, names[idx])
		} else {
			spans = []span{{strings.Repeat(" ", listW), m.theme.Normal}}
		}
		if i < len(boxSpans) {
			spans = append(spans, span{"  ", m.theme.Normal})
			spans = append(spans, boxSpans[i]...)
		}
		rows = append(rows, m.setupRow(l, spans...))
	}
	return rows
}

// setupThemeSample is a short transcript that exercises the theme roles a
// session uses: the user band, tool dots, muted results, diff tints, inline
// code, and errors.
func (m *ui) setupThemeSample() []setupBoxLine {
	g := m.blocks
	t := m.theme
	return []setupBoxLine{
		{band: t.BgUser, spans: []span{{g.User + " ", t.Normal}, {"add retries to fetch", t.Normal}}},
		{},
		{spans: []span{{g.ToolHeader + " ", t.Success}, {"Read", t.Normal.Bold(true)}, {"(net/fetch.go)", t.Normal}}},
		{spans: []span{{"  " + g.Result + "  ", t.Muted}, {"Read 84 lines", t.Muted}}},
		{spans: []span{{g.ToolHeader + " ", t.Success}, {"Update", t.Normal.Bold(true)}, {"(net/fetch.go)", t.Normal}}},
		{spans: []span{{"  " + g.Result + "  ", t.Muted}, {"1 addition, 1 removal", t.Muted}}},
		{band: t.BgDiffRemove, spans: []span{{"     41 - ", t.Muted}, {"return get(url)", t.Normal}}},
		{band: t.BgDiffAdd, spans: []span{{"     41 + ", t.Muted}, {"return retry(get, url)", t.Normal}}},
		{spans: []span{{g.Assistant + " ", t.Normal}, {"Wrapped ", t.Normal}, {"get", withBase(t.Accent, t.BgCode)}, {" in a 3-try retry.", t.Normal}}},
		{spans: []span{{g.Error + " ", t.Error}, {"go test: 1 failing test", t.Error}}},
	}
}

// setupBoxLine is one content row of a framed box; band paints the row.
type setupBoxLine struct {
	band  lipgloss.Style
	spans []span
}

// setupBoxSpans builds the rows of a rounded box width cells wide.
func (m *ui) setupBoxSpans(width int, title string, border lipgloss.Style, lines []setupBoxLine) [][]span {
	g := m.blocks
	inner := max(0, width-4)
	top := g.TopLeft + g.Horizontal + " " + title + " "
	top += strings.Repeat(g.Horizontal, max(0, width-1-runewidth.StringWidth(top))) + g.TopRight
	out := [][]span{{{top, border}}}
	for _, line := range lines {
		band := line.band
		if band.GetBackground() == nil {
			band = m.theme.Base
		}
		styled := make([]span, len(line.spans))
		for i, sp := range line.spans {
			styled[i] = span{sp.text, withBase(sp.style, band)}
		}
		row := []span{{g.Vertical + " ", border}}
		row = append(row, m.fitSpans(styled, inner, band)...)
		row = append(row, span{" " + g.Vertical, border})
		out = append(out, row)
	}
	bottom := g.BottomLeft + strings.Repeat(g.Horizontal, max(0, width-2)) + g.BottomRight
	return append(out, []span{{bottom, border}})
}

// setupItem renders one list row width cells wide: a cursor marker, the
// label, and right-aligned detail spans. The cursor row carries the
// selection tint (the neutral user band where the theme has no tint) and
// the > marker, so selection never depends on color alone.
func (m *ui) setupItem(width int, selected bool, label string, right ...span) []span {
	band, marker, labelStyle := m.theme.Base, "  ", m.theme.Normal
	if selected {
		band, marker, labelStyle = m.setupSelectionBand(), "> ", m.theme.Selected
	}
	on := func(s lipgloss.Style) lipgloss.Style { return withBase(s, band) }
	// Details give way from the front (context before "recommended") until
	// the label keeps a readable width.
	need := min(12, runewidth.StringWidth(label))
	for len(right) > 0 && width-2-spansWidth(right)-2 < need {
		right = right[1:]
		for len(right) > 0 && strings.TrimSpace(right[0].text) == "" {
			right = right[1:]
		}
	}
	rw := spansWidth(right)
	room := width - 2 - rw
	if rw > 0 {
		room -= 2
	}
	text := clipVisible(label, max(0, room), m.blocks.Ellipsis)
	spans := []span{{marker, on(labelStyle)}, {text, on(labelStyle)}}
	if gap := width - 2 - runewidth.StringWidth(text) - rw; gap > 0 {
		spans = append(spans, span{strings.Repeat(" ", gap), band})
	}
	for _, r := range right {
		spans = append(spans, span{r.text, on(r.style)})
	}
	return m.fitSpans(spans, width, band)
}

// setupSelectionBand is the cursor row background: the theme's selection
// tint, else the user band (default family).
func (m *ui) setupSelectionBand() lipgloss.Style {
	if bg := m.theme.Selected.GetBackground(); bg != nil {
		if _, none := bg.(lipgloss.NoColor); !none {
			return lipgloss.NewStyle().Background(bg)
		}
	}
	return m.theme.BgUser
}

// setupErrorRows counts the rows appendSetupError adds.
func (m *ui) setupErrorRows(l setupLayout) int {
	if m.setup.err == "" {
		return 0
	}
	return len(m.appendSetupError(l, nil))
}

// appendSetupError adds the error block: a spacer row (except on short
// terminals), then the ✗ glyph and the message wrapped under its text.
func (m *ui) appendSetupError(l setupLayout, rows []string) []string {
	if m.setup.err == "" {
		return rows
	}
	if !l.compact {
		rows = append(rows, m.setupRow(l))
	}
	return append(rows, m.setupWrapped(l, span{m.blocks.Error + " ", m.theme.Error}, m.setup.err, m.theme.Error)...)
}

// setupWrapped renders lead followed by text word-wrapped to the column,
// continuation rows hanging under the text.
func (m *ui) setupWrapped(l setupLayout, lead span, text string, style lipgloss.Style) []string {
	lw := runewidth.StringWidth(lead.text)
	var rows []string
	for i, line := range wrapWords(text, max(1, l.col-lw)) {
		if i > 0 {
			lead = span{strings.Repeat(" ", lw), m.theme.Normal}
		}
		rows = append(rows, m.setupRow(l, lead, span{line, style}))
	}
	return rows
}

// setupFooter is the key-hint row pinned to the bottom: keys in the help
// style, actions muted, dropped from the end when the row is too narrow.
func (m *ui) setupFooter(l setupLayout) string {
	up, down := "↑", "↓"
	if m.conn.ASCII {
		up, down = "up", "down"
	}
	move := up + down
	if m.conn.ASCII {
		move = up + "/" + down
	}
	var hints [][2]string
	switch m.setup.stage {
	case setupProvider:
		hints = [][2]string{{move, "move"}, {"enter", "select"}, {"type", "filter"}}
		if len(m.setup.filter) > 0 {
			hints = append(hints, [2]string{"esc", "clear"})
		}
	case setupKey:
		action := "check key"
		if len(m.setup.keyInput) == 0 && setupEnvKeySet(model.Providers[m.setup.cursor]) {
			action = "use env key"
		}
		hints = [][2]string{{"enter", action}, {"esc", "back"}, {"ctrl+u", "clear"}}
	case setupChecking:
		hints = [][2]string{{"esc", "cancel"}}
	case setupLogin:
		if m.setup.checking {
			hints = [][2]string{{"esc", "cancel"}}
		} else {
			hints = [][2]string{{"enter", "retry"}, {"esc", "back"}}
		}
	case setupPlanNotice:
		hints = [][2]string{{"enter", "Got it"}, {"esc", "back"}}
	case setupModel:
		hints = [][2]string{{move, "move"}, {"enter", "select"}, {"type", "filter"}}
		if len(m.setup.filter) > 0 {
			hints = append(hints, [2]string{"esc", "clear"})
		} else {
			hints = append(hints, [2]string{"esc", "back"})
		}
	case setupTheme:
		hints = [][2]string{{move, "preview"}, {"enter", "apply"}, {"esc", "back"}}
	}
	hints = append(hints, [2]string{"ctrl+c", "quit"})
	sep := "   "
	if l.col < 60 {
		sep = "  "
	}
	var spans []span
	for _, h := range hints {
		next := []span{{h[0], m.theme.Help}, {" " + h[1], m.theme.Muted}}
		if len(spans) > 0 {
			next = append([]span{{sep, m.theme.Muted}}, next...)
		}
		if spansWidth(spans)+spansWidth(next) > l.col {
			break
		}
		spans = append(spans, next...)
	}
	return m.setupRow(l, spans...)
}

// caretGlyph is the input caret: a block while the blink phase is on.
func (m *ui) caretGlyph() string {
	if m.caretOn || m.caretTyped {
		return "█"
	}
	return " "
}

// setupRow lays spans into the content column and paints the margins and
// any unused width on the canvas.
func (m *ui) setupRow(l setupLayout, spans ...span) string {
	var b strings.Builder
	b.WriteString(m.theme.Base.Render(strings.Repeat(" ", l.left)))
	for _, sp := range m.fitSpans(spans, l.col, m.theme.Base) {
		b.WriteString(withBase(sp.style, m.theme.Base).Render(sp.text))
	}
	if tail := m.width - l.left - l.col; tail > 0 {
		b.WriteString(m.theme.Base.Render(strings.Repeat(" ", tail)))
	}
	return b.String()
}

// fitSpans clips spans to width cells, escaping hidden runes, and pads the
// rest with spaces in pad.
func (m *ui) fitSpans(spans []span, width int, pad lipgloss.Style) []span {
	out := make([]span, 0, len(spans)+1)
	used := 0
	for _, sp := range spans {
		room := width - used
		if room <= 0 {
			break
		}
		text := clipVisible(sp.text, room, m.blocks.Ellipsis)
		if text == "" {
			continue
		}
		out = append(out, span{text, sp.style})
		used += runewidth.StringWidth(text)
	}
	if used < width {
		out = append(out, span{strings.Repeat(" ", width-used), pad})
	}
	return out
}

// justify places right at the end of a width-cell row after left.
func justify(width int, left, right []span) []span {
	gap := width - spansWidth(left) - spansWidth(right)
	if gap < 2 {
		return left
	}
	out := append([]span{}, left...)
	out = append(out, span{strings.Repeat(" ", gap), lipgloss.NewStyle()})
	return append(out, right...)
}

// clipVisible escapes control and zero-width runes (as fit does) and bounds
// the result to width cells, ending in ellipsis when anything was cut.
func clipVisible(text string, width int, ellipsis string) string {
	var b strings.Builder
	for _, r := range text {
		if hiddenReviewRune(r) {
			fmt.Fprintf(&b, "\\u%04X", r)
			continue
		}
		b.WriteRune(r)
	}
	s := b.String()
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if strings.TrimSpace(s) == "" {
		return strings.Repeat(" ", width)
	}
	return toolClipCells(s, width, ellipsis)
}

func spansWidth(spans []span) int {
	w := 0
	for _, sp := range spans {
		w += runewidth.StringWidth(sp.text)
	}
	return w
}

func indexOf(values []int, v int) int {
	for i, x := range values {
		if x == v {
			return i
		}
	}
	return -1
}
