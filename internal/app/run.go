package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"lisa/internal/mcp"
	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/repository"
	"lisa/internal/session"
	"lisa/internal/tui"
	lisaui "lisa/internal/ui"
)

const usageHeader = `Usage: lisa [options] [repository]

Start Lisa, an agent harness terminal UI, in a repository. Lisa talks directly
to the configured model provider's OpenAI-compatible API with your own API key
(BYOK); it hosts no models and bundles no local inference server. Lisa checks
the connection at startup and before each agent run, and reports a dead or
revoked key before any prompt is sent.

Providers (choose one with --provider; with none given, an interactive run
offers first-run setup):
`

const usageFooter = `
Options:
  --provider NAME   Provider from the list above (or LISA_PROVIDER).
  --api-key KEY     API key for a hosted provider; also LISA_API_KEY. An
                    explicitly passed key is stored in the private state
                    directory (providers.json, mode 0600) for later runs.
  --model NAME      Model to use. Optional: without it, Lisa uses the first
                    model the endpoint reports (local servers), or the
                    provider's documented default. Hosted model IDs are
                    provider-specific, e.g. "anthropic/claude-3.5-sonnet".
  --endpoint URL    OpenAI-compatible base URL; overrides the provider default.
                    Plain HTTP is allowed only for loopback hosts. Endpoints
                    outside the list run with a visible "unverified" warning.
  --sessions        List saved sessions for the selected repository; no model needed.
  --resume ID       Resume a saved session for the selected repository.
  --device-login    Sign in to the selected OAuth provider (e.g. chatgpt)
                    headlessly and store the login; no TUI or repository needed.
  --version         Print the build version and exit.
  --help            Print this help and exit.

First-run setup: launching with no provider configured (interactive terminal)
prompts for a provider, your API key, and a model, and stores the choice.
ChatGPT signs in through your browser at first run instead of using an API
key; the login is stored for later runs.

Environment: LISA_MODEL, LISA_ENDPOINT, LISA_PROVIDER, LISA_API_KEY,
LISA_STATE_DIR (private storage directory; default is the OS user
configuration directory's "lisa" child). Each provider also accepts its own
LISA_<NAME>_API_KEY (see the provider list above).

Examples:
  lisa ~/projects/app                                        # first-run setup
  lisa --provider opencode-go ~/projects/app                 # stored key, default model
  lisa --provider openai --api-key sk-... ~/projects/app

Put options before the repository path. Lisa needs an interactive terminal.

Composer editing keys: Ctrl+W / Ctrl+Backspace / Alt+Backspace delete the
previous word (with its spaces), Ctrl+Delete or Alt+D the next word;
Alt+B/Alt+F move by word; Ctrl+U/Ctrl+K kill to the start/end and Ctrl+Y
yanks; Ctrl+T transposes. Alt+Return (or Ctrl+Return/Shift+Return where the
terminal sends them; Ctrl+J always) inserts a newline; Enter sends and
flattens newlines to spaces. Esc clears an idle draft. Tool output and model
reasoning render in the theme's muted color.
`

const contextWindowMetadataTimeout = 2 * time.Second

// listModelDetailsFunc is the bounded, best-effort startup metadata seam.
// Keeping it separate from connection checks lets tests prove startup metadata
// failures do not affect session setup.
var listModelDetailsFunc = model.ListModelsWithDetails

// discoverContextWindow fetches reported metadata for the configured model.
// A missing, invalid, or unavailable value is unknown; callers can still use
// the stored override or documented catalog. This never sends a prompt.
func discoverContextWindow(modelID, endpoint, apiKey string) map[string]int64 {
	if strings.TrimSpace(modelID) == "" || strings.TrimSpace(endpoint) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), contextWindowMetadataTimeout)
	defer cancel()
	details, err := listModelDetailsFunc(ctx, endpoint, apiKey)
	if err != nil {
		return nil
	}
	for _, detail := range details {
		if detail.ID == modelID && detail.ContextWindow > 0 {
			return map[string]int64{detail.ID: detail.ContextWindow}
		}
	}
	return nil
}

// usageText builds the help text with the provider block generated from the
// provider table so help cannot drift from the accepted list.
func usageText() string {
	var b strings.Builder
	b.WriteString(usageHeader)
	for _, p := range model.Providers {
		key := p.KeyEnv
		if !p.Hosted {
			key = "no key needed"
		}
		if p.Auth == model.AuthOAuth {
			key = "OAuth login"
		}
		line := fmt.Sprintf("  %-14s %-52s key: %s", p.Name, p.BaseURL, key)
		if p.DefaultModel != "" {
			line += fmt.Sprintf("  default model: %s", p.DefaultModel)
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	b.WriteString(usageFooter)
	return b.String()
}

// Run validates startup settings and runs the interactive terminal UI.
func Run(args []string, stdout, stderr io.Writer) int {
	model.UserAgent = "lisa/" + tui.Version
	flags := flag.NewFlagSet("lisa", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stdout, usageText()) }
	endpoint := flags.String("endpoint", "", "OpenAI-compatible endpoint (overrides the provider default)")
	name := flags.String("model", os.Getenv("LISA_MODEL"), "model name")
	providerName := flags.String("provider", os.Getenv("LISA_PROVIDER"), "provider from the predefined accepted list")
	apiKey := flags.String("api-key", os.Getenv("LISA_API_KEY"), "API key for a hosted provider (stored in the private state directory)")
	listSessions := flags.Bool("sessions", false, "list sessions for the selected repository")
	resumeID := flags.String("resume", "", "resume a previous session ID")
	themeFlag := flags.String("theme", os.Getenv("LISA_THEME"), "color theme (see --help list; stored in config.json when set)")
	nerdFlag := flags.Bool("nerd-fonts", os.Getenv("LISA_NERD") == "1", "use Nerd Font glyphs for status markers")
	showVersion := flags.Bool("version", false, "print the build version")
	deviceLogin := flags.Bool("device-login", false, "sign in to the selected OAuth provider headlessly and store the login")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "Lisa %s\n", tui.Version)
		return 0
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "lisa: expected at most one repository path")
		return 2
	}
	path := "."
	if *listSessions && *resumeID != "" {
		fmt.Fprintln(stderr, "lisa: choose either --sessions or --resume")
		return 2
	}
	if flags.NArg() == 1 {
		path = flags.Arg(0)
	}
	root, err := resolveRoot(path)
	if err != nil {
		fmt.Fprintf(stderr, "lisa: %v\n", err)
		return 2
	}
	stateDir := os.Getenv("LISA_STATE_DIR")
	if stateDir == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintf(stderr, "lisa: locate local session storage: %v\n", err)
			return 2
		}
		stateDir = filepath.Join(configDir, "lisa")
	}
	store, err := session.Open(stateDir, root)
	if err != nil {
		fmt.Fprintf(stderr, "lisa: session storage: %v\n", err)
		return 2
	}
	defer store.Close()
	if *listSessions {
		summaries, err := store.List()
		if err != nil {
			fmt.Fprintf(stderr, "lisa: list sessions: %v\n", err)
			return 1
		}
		for _, item := range summaries {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.ID, item.Updated.Local().Format("2006-01-02 15:04"), strconv.Quote(item.Title))
		}
		return 0
	}
	persistKey := false
	providerExplicit := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "api-key" {
			persistKey = true
		}
		if f.Name == "provider" {
			providerExplicit = true
		}
	})
	stored, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		fmt.Fprintf(stderr, "lisa: %v\n", err)
		return 2
	}
	// Precedence: --provider flag (which includes LISA_PROVIDER) > stored
	// first-run config. A stored model applies only when the provider itself
	// came from storage and no model was given. The theme follows the same
	// rule: explicit --theme/LISA_THEME > stored config > default.
	effectiveProvider := *providerName
	if !providerExplicit && *providerName == "" && stored.Provider != "" {
		effectiveProvider = stored.Provider
		if *name == "" && stored.Model != "" {
			*name = stored.Model
		}
	}
	if *deviceLogin {
		return deviceLoginFlow(effectiveProvider, stateDir, stdout, stderr)
	}
	themeName := *themeFlag
	themeExplicit := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "theme" {
			themeExplicit = true
		}
	})
	if !themeExplicit && themeName == "" && stored.Theme != "" {
		themeName = stored.Theme
	}
	if _, ok := lisaui.Named(themeName, true); !ok && themeName != "" && themeName != "default" {
		fmt.Fprintf(stderr, "lisa: unknown theme %q; using default (run lisa --help for the list)\n", themeName)
		themeName = ""
	}
	composerStyle := ""
	if stored.Composer != nil {
		composerStyle = stored.Composer.Style
	}
	var statusLine providers.StoredStatusLineConfig
	if stored.StatusLine != nil {
		statusLine = *stored.StatusLine
	}
	// First-run setup: nothing configured anywhere. An interactive terminal
	// walks the user through provider, key, and model; anything else fails
	// clearly.
	setupNeeded := effectiveProvider == "" && *endpoint == "" && os.Getenv("LISA_ENDPOINT") == ""
	output, isFile := stdout.(*os.File)
	interactive := isatty.IsTerminal(os.Stdin.Fd()) && isFile && isatty.IsTerminal(output.Fd())
	if setupNeeded && !interactive {
		fmt.Fprintln(stderr, "lisa: no provider configured; set --provider or LISA_PROVIDER (see --help), or run Lisa in an interactive terminal to set one up")
		return 2
	}
	var chosen, display string
	var verified bool
	var key string
	var res providers.ResolvedProvider
	var selected model.Provider
	var modelName string
	var client *model.Client
	if !setupNeeded {
		res, err = providers.ResolveProvider(effectiveProvider, *endpoint, *apiKey, persistKey, stateDir)
		if err != nil {
			fmt.Fprintf(stderr, "lisa: %v\n", err)
			return 2
		}
		chosen, verified, display, key = res.Endpoint, res.Verified, res.Display, res.Key
		selected, _ = model.LookupProvider(effectiveProvider)
		modelName, err = resolveModel(*name, selected, chosen, key)
		if err != nil {
			fmt.Fprintf(stderr, "lisa: %v\n", err)
			return 2
		}
		if res.OAuth {
			client, err = model.NewOAuth(res.Endpoint, modelName, model.ChatGPTIssuer, model.ChatGPTClientID, res.Creds)
		} else {
			client, err = model.New(chosen, modelName, key)
		}
		if err != nil {
			fmt.Fprintf(stderr, "lisa: %v\n", err)
			return 2
		}
		if res.OAuth {
			// By the time NewOAuth returns, a stored login exists, so the
			// provider name must resolve; providers.ResolveProvider already validated
			// it. The saver persists every refreshed token set.
			client.SetOAuthSaver(func(c model.OAuthCredentials) error {
				return providers.StoreOAuth(stateDir, selected.Name, c)
			})
		}
	}
	repo, err := repository.New(root)
	if err != nil {
		fmt.Fprintf(stderr, "lisa: %v\n", err)
		return 2
	}
	mcpServers, err := mcp.NewMcpManager(stateDir, model.UserAgent)
	if err != nil {
		fmt.Fprintf(stderr, "lisa: %v\n", err)
		return 2
	}
	defer mcpServers.Stop()
	if !interactive {
		fmt.Fprintln(stderr, "lisa: interactive terminal required")
		return 2
	}
	var startupErr error
	var contextWindows map[string]int64
	if client != nil {
		// Startup connection check: verify the provider answers before the
		// first prompt. A failure is reported in the interface, not fatal, so
		// the session stays available for another attempt. In setup mode the
		// connection is verified interactively instead.
		startupErr = client.EnsureConnected(context.Background())
		if !res.OAuth {
			contextWindows = discoverContextWindow(modelName, chosen, key)
		}
	}
	var snapshot session.Snapshot
	if *resumeID != "" {
		snapshot, err = store.Load(*resumeID)
	} else {
		snapshot, err = store.Create()
	}
	if err != nil {
		fmt.Fprintf(stderr, "lisa: session: %v\n", err)
		return 2
	}
	if client != nil && selected.SessionHeader != "" {
		// Providers such as OpenCode route and cache per conversation; the
		// stable SQLite session ID identifies the conversation for its life.
		client.SetSessionHeader(selected.SessionHeader)
		client.SetSession(snapshot.ID)
	}
	program := tea.NewProgram(tui.NewUI(root, repo, client, modelName, providers.Connection{
		Provider: display, ProviderCanonical: selected.Name, Verified: verified, Err: startupErr,
		Setup: setupNeeded, Theme: themeName, ComposerStyle: composerStyle, StatusLine: statusLine,
		ContextWindows: contextWindows, ContextWindowOverrides: stored.ContextWindows,
		Nerd: *nerdFlag || os.Getenv("LISA_NERD") == "1", Mcp: mcpServers,
	}, stateDir, store, snapshot), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithInput(os.Stdin), tea.WithOutput(stdout))
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(stderr, "lisa: terminal: %v\n", err)
		return 1
	}
	return 0
}

// resolveModel returns the model to use. An explicit name wins. Without one, a
// hosted provider falls back to its documented default; the local and custom
// endpoints fall back to the first model the endpoint reports, so a zero-
// configuration local run needs no LISA_MODEL at all.
func resolveModel(name string, p model.Provider, endpoint, key string) (string, error) {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name), nil
	}
	if p.Hosted {
		if p.DefaultModel != "" {
			return p.DefaultModel, nil
		}
		return "", fmt.Errorf("model is required for provider %s; set LISA_MODEL or --model", p.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ids, err := model.ListModels(ctx, endpoint, key)
	if err != nil {
		return "", fmt.Errorf("model is required; set LISA_MODEL or --model (model list failed: %v)", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("model is required; set LISA_MODEL or --model (the endpoint at %s reports no models)", endpoint)
	}
	return ids[0], nil
}

func resolveRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve repository: %w", err)
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve repository: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("inspect repository: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("repository path is not a directory")
	}
	return root, nil
}

// deviceLoginFlow runs the headless device authorization flow for one OAuth
// provider and stores the resulting login in the private state directory. It
// needs no interactive terminal, no repository, and no TUI: printing to stdout
// keeps the code and verification URL visible even when output is piped.
func deviceLoginFlow(providerName, stateDir string, stdout, stderr io.Writer) int {
	name := strings.TrimSpace(providerName)
	if name == "" {
		fmt.Fprintln(stderr, "lisa: --device-login needs a provider; choose one with --provider (e.g. --provider chatgpt)")
		return 2
	}
	p, ok := model.LookupProvider(name)
	if !ok {
		fmt.Fprintf(stderr, "lisa: unknown provider %q (see --help)\n", name)
		return 2
	}
	if p.Auth != model.AuthOAuth {
		fmt.Fprintln(stderr, "lisa: --device-login applies to OAuth providers (chatgpt)")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tokenSet, err := model.DeviceLogin(ctx, model.ChatGPTIssuer, model.ChatGPTClientID, model.UserAgent, func(userCode, verifyURL string) error {
		fmt.Fprintf(stdout, "Open %s and enter code: %s\n", verifyURL, userCode)
		fmt.Fprintln(stdout, "Waiting for approval… (Ctrl+C aborts)")
		return nil
	}, nil)
	if err != nil {
		fmt.Fprintf(stderr, "lisa: %v\n", err)
		return 1
	}
	creds := tokenSet.Credentials()
	if err := providers.StoreOAuth(stateDir, p.Name, creds); err != nil {
		fmt.Fprintf(stderr, "lisa: store login: %v\n", err)
		return 1
	}
	account := creds.AccountID
	if account == "" {
		account = "unknown account"
	}
	fmt.Fprintf(stdout, "Signed in as %s; login stored in %s\n", account, providers.KeyFilePath(stateDir))
	return 0
}
