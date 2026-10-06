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

	"likha/internal/mcp"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
	"likha/internal/tui"
	likhaui "likha/internal/ui"
)

const usageHeader = `Usage: likha [options] [repository]

Start Likha, an agent harness terminal UI, in a repository. Likha talks directly
to the configured model provider's OpenAI-compatible API with your own API key
(BYOK); it hosts no models and bundles no local inference server. Likha checks
the connection at startup and before each agent run, and reports a dead or
revoked key before any prompt is sent.

Providers (choose one with --provider; with none given, an interactive run
offers first-run setup):
`

const usageFooter = `
Options:
  --provider NAME   Provider from the list above (or LIKHA_PROVIDER).
  --api-key KEY     API key for a hosted provider; also LIKHA_API_KEY. An
                    explicitly passed key is stored in the private state
                    directory (providers.json, mode 0600) for later runs.
  --model NAME      Model to use. Optional: without it, Likha uses the first
                    model the endpoint reports (local servers), or the
                    provider's documented default. Hosted model IDs are
                    provider-specific, e.g. "anthropic/claude-3.5-sonnet".
  --endpoint URL    OpenAI-compatible base URL; overrides the provider default.
                    Plain HTTP is allowed only for loopback hosts. Endpoints
                    outside the list run with a visible "unverified" warning.
  --sessions        List saved sessions for the selected repository; no model needed.
  --resume ID       Resume a saved session for the selected repository.
  --login           Open your system browser for ChatGPT sign-in and consent,
                    validate account-specific models, and store the login.
                    Defaults to --provider chatgpt; no TUI or repository needed.
  --device-login    Unsupported legacy flag. Use --provider chatgpt --login.
  --debug-models    Log model IDs and context metadata to
                    model-metadata.log (also LIKHA_DEBUG_MODELS=1).
  --ascii           Draw transcript block glyphs and the composer box in plain
                    ASCII instead of Unicode (also LIKHA_ASCII=1).
  --version         Print the build version and exit.
  --help            Print this help and exit.

First-run setup: launching with no provider configured (interactive terminal)
prompts for a provider, your API key, and a model, and stores the choice.
Selecting ChatGPT automatically opens your browser; after sign-in and consent,
setup continues automatically. Usage shares your ChatGPT plan allowance and
limits. Credits apply only if opted in in ChatGPT Settings; API billing is separate.

Environment: LIKHA_MODEL, LIKHA_ENDPOINT, LIKHA_PROVIDER, LIKHA_API_KEY,
LIKHA_DEBUG_MODELS, LIKHA_ASCII, LIKHA_STATE_DIR (private storage directory;
default is $XDG_CONFIG_HOME/likha or ~/.config/likha, including on macOS). Each provider
also accepts its own LIKHA_<NAME>_API_KEY (see the provider list above).

Examples:
  likha ~/projects/app                                        # first-run setup
  likha --provider opencode-go ~/projects/app                 # stored key, default model
  likha --provider openai --api-key sk-... ~/projects/app

Put options before the repository path. Likha needs an interactive terminal.

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
	return discoverContextWindowWithObserver(modelID, endpoint, apiKey, "", nil)
}

func discoverContextWindowWithObserver(modelID, endpoint, apiKey, provider string, observe providers.ModelMetadataObserver) map[string]int64 {
	if strings.TrimSpace(modelID) == "" || strings.TrimSpace(endpoint) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), contextWindowMetadataTimeout)
	defer cancel()
	start := time.Now()
	details, err := listModelDetailsFunc(ctx, endpoint, apiKey)
	if observe != nil {
		observe(provider, "startup /models", time.Since(start), details, err)
	}
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
	model.UserAgent = "likha/" + tui.Version
	flags := flag.NewFlagSet("likha", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stdout, usageText()) }
	endpoint := flags.String("endpoint", "", "OpenAI-compatible endpoint (overrides the provider default)")
	name := flags.String("model", os.Getenv("LIKHA_MODEL"), "model name")
	providerName := flags.String("provider", os.Getenv("LIKHA_PROVIDER"), "provider from the predefined accepted list")
	apiKey := flags.String("api-key", os.Getenv("LIKHA_API_KEY"), "API key for a hosted provider (stored in the private state directory)")
	listSessions := flags.Bool("sessions", false, "list sessions for the selected repository")
	resumeID := flags.String("resume", "", "resume a previous session ID")
	themeFlag := flags.String("theme", os.Getenv("LIKHA_THEME"), "color theme (see --help list; stored in config.json when set)")
	nerdFlag := flags.Bool("nerd-fonts", os.Getenv("LIKHA_NERD") == "1", "use Nerd Font glyphs for status markers")
	asciiFlag := flags.Bool("ascii", os.Getenv("LIKHA_ASCII") == "1", "draw transcript block glyphs and the composer box in plain ASCII")
	debugModels := flags.Bool("debug-models", os.Getenv("LIKHA_DEBUG_MODELS") == "1", "log connected model metadata to the private state directory")
	showVersion := flags.Bool("version", false, "print the build version")
	deviceLogin := flags.Bool("device-login", false, "unsupported legacy login; use --provider chatgpt --login")
	login := flags.Bool("login", false, "automatically open the browser for ChatGPT authorization and store the login")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "Likha %s\n", tui.Version)
		return 0
	}
	if *deviceLogin {
		fmt.Fprintln(stderr, "likha: --device-login is no longer supported; use likha --provider chatgpt --login for browser authorization, or select ChatGPT in the interactive setup")
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "likha: expected at most one repository path")
		return 2
	}
	path := "."
	if *listSessions && *resumeID != "" {
		fmt.Fprintln(stderr, "likha: choose either --sessions or --resume")
		return 2
	}
	if flags.NArg() == 1 {
		path = flags.Arg(0)
	}
	if *login {
		if *listSessions || *resumeID != "" {
			fmt.Fprintln(stderr, "likha: --login cannot be combined with --sessions or --resume")
			return 2
		}
		loginProvider := strings.TrimSpace(*providerName)
		if loginProvider == "" {
			loginProvider = "chatgpt"
		}
		if p, ok := model.LookupProvider(loginProvider); ok && p.Auth == model.AuthOAuth {
			if err := validateOAuthEndpoint(p, *endpoint); err != nil {
				fmt.Fprintf(stderr, "likha: %v\n", err)
				return 2
			}
		}
		stateDir, err := resolveStateDir(stderr)
		if err != nil {
			fmt.Fprintf(stderr, "likha: locate login storage: %v\n", err)
			return 2
		}
		return browserLoginFlow(loginProvider, stateDir, stdout, stderr)
	}
	root, err := resolveRoot(path)
	if err != nil {
		fmt.Fprintf(stderr, "likha: %v\n", err)
		return 2
	}
	stateDir, err := resolveStateDir(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "likha: locate local session storage: %v\n", err)
		return 2
	}
	store, err := session.Open(stateDir, root)
	if err != nil {
		fmt.Fprintf(stderr, "likha: session storage: %v\n", err)
		return 2
	}
	defer store.Close()
	migrateToolsConfig(stateDir, stderr)
	if *listSessions {
		summaries, err := store.List()
		if err != nil {
			fmt.Fprintf(stderr, "likha: list sessions: %v\n", err)
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
		fmt.Fprintf(stderr, "likha: %v\n", err)
		return 2
	}
	// Precedence: --provider flag (which includes LIKHA_PROVIDER) > stored
	// first-run config. A stored model applies only when the provider itself
	// came from storage and no model was given. The theme follows the same
	// rule: explicit --theme/LIKHA_THEME > stored config > default.
	effectiveProvider := *providerName
	if !providerExplicit && *providerName == "" && stored.Provider != "" {
		effectiveProvider = stored.Provider
		if *name == "" && stored.Model != "" {
			*name = stored.Model
		}
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
	if _, ok := likhaui.Named(themeName, true); !ok && themeName != "" && themeName != "default" {
		fmt.Fprintf(stderr, "likha: unknown theme %q; using default (run likha --help for the list)\n", themeName)
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
	setupNeeded := effectiveProvider == "" && *endpoint == "" && os.Getenv("LIKHA_ENDPOINT") == ""
	output, isFile := stdout.(*os.File)
	interactive := isatty.IsTerminal(os.Stdin.Fd()) && isFile && isatty.IsTerminal(output.Fd())
	if setupNeeded && !interactive {
		fmt.Fprintln(stderr, "likha: no provider configured; set --provider or LIKHA_PROVIDER (see --help), or run Likha in an interactive terminal to set one up")
		return 2
	}
	var chosen, display string
	var verified bool
	var key string
	var res providers.ResolvedProvider
	var selected model.Provider
	var modelName string
	var client *model.Client
	selected, _ = model.LookupProvider(effectiveProvider)
	if selected.Auth == model.AuthOAuth {
		if err := validateOAuthEndpoint(selected, *endpoint); err != nil {
			fmt.Fprintf(stderr, "likha: %v\n", err)
			return 2
		}
		needsLogin, err := oauthSetupRequired(selected, stateDir, interactive)
		if err != nil {
			fmt.Fprintf(stderr, "likha: %v\n", err)
			return 2
		}
		if needsLogin {
			setupNeeded, display, verified = true, selected.DisplayName, true
		}
	}
	if !setupNeeded {
		res, err = providers.ResolveProvider(effectiveProvider, *endpoint, *apiKey, persistKey, stateDir)
		if err != nil {
			fmt.Fprintf(stderr, "likha: %v\n", err)
			return 2
		}
		chosen, verified, display, key = res.Endpoint, res.Verified, res.Display, res.Key
		selected, _ = model.LookupProvider(effectiveProvider)
		if res.OAuth {
			modelName = strings.TrimSpace(*name)
			client, err = model.NewOAuth(res.Endpoint, modelName, res.Creds.Issuer, res.Creds.ClientID, res.Creds)
		} else {
			modelName, err = resolveModel(*name, selected, chosen, key)
			if err != nil {
				fmt.Fprintf(stderr, "likha: %v\n", err)
				return 2
			}
			client, err = model.New(chosen, modelName, key)
		}
		if err != nil {
			fmt.Fprintf(stderr, "likha: %v\n", err)
			return 2
		}
		if res.OAuth {
			bindOAuthStorage(client, stateDir, selected.Name)
		}
	}
	repo, err := repository.New(root)
	if err != nil {
		fmt.Fprintf(stderr, "likha: %v\n", err)
		return 2
	}
	mcpServers, err := mcp.NewMcpManager(stateDir, model.UserAgent)
	if err != nil {
		fmt.Fprintf(stderr, "likha: %v\n", err)
		return 2
	}
	defer mcpServers.Stop()
	if !interactive {
		fmt.Fprintln(stderr, "likha: interactive terminal required")
		return 2
	}
	var metadataLog *modelMetadataLog
	var metadataObserver providers.ModelMetadataObserver
	if *debugModels {
		metadataLog, err = openModelMetadataLog(stateDir)
		if err != nil {
			fmt.Fprintf(stderr, "likha: model metadata diagnostics unavailable: %v\n", err)
		} else {
			defer metadataLog.close()
			metadataObserver = metadataLog.observer()
			fmt.Fprintf(stderr, "likha: model metadata debug log: %s\n", metadataLog.path)
		}
	}
	var startupErr error
	var contextWindows map[string]int64
	if client != nil {
		// Startup connection check: verify the provider answers before the
		// first prompt. A failure is reported in the interface, not fatal, so
		// the session stays available for another attempt. In setup mode the
		// connection is verified interactively instead.
		if res.OAuth {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			start := time.Now()
			var details []model.ModelDetails
			modelName, contextWindows, details, startupErr = discoverOAuthModel(ctx, client, modelName)
			cancel()
			if metadataObserver != nil {
				metadataObserver(selected.Name, "startup authenticated /models", time.Since(start), details, startupErr)
			}
			if startupErr == nil {
				startupErr = client.SetModel(modelName)
			}
		} else {
			startupErr = client.EnsureConnected(context.Background())
			providerID := selected.Name
			if providerID == "" {
				providerID = "custom"
			}
			contextWindows = discoverContextWindowWithObserver(modelName, chosen, key, providerID, metadataObserver)
		}
	}
	var snapshot session.Snapshot
	if *resumeID != "" {
		snapshot, err = store.Load(*resumeID)
	} else {
		snapshot, err = store.Create()
	}
	if err != nil {
		fmt.Fprintf(stderr, "likha: session: %v\n", err)
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
		ContextWindows: contextWindows, ContextWindowOverrides: stored.ContextWindows, ModelMetadataObserver: metadataObserver,
		Nerd: *nerdFlag || os.Getenv("LIKHA_NERD") == "1", ASCII: asciiGlyphs(*asciiFlag), Mcp: mcpServers,
	}, stateDir, store, snapshot), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithInput(os.Stdin), tea.WithOutput(stdout))
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(stderr, "likha: terminal: %v\n", err)
		return 1
	}
	return 0
}

// asciiGlyphs reports whether the plain-ASCII block glyph set applies: the
// --ascii flag or LIKHA_ASCII=1, mirroring --nerd-fonts / LIKHA_NERD.
func asciiGlyphs(flagValue bool) bool {
	return flagValue || os.Getenv("LIKHA_ASCII") == "1"
}

// resolveModel returns the model to use. An explicit name wins. Without one, a
// hosted provider falls back to its documented default; the local and custom
// endpoints fall back to the first model the endpoint reports, so a zero-
// configuration local run needs no LIKHA_MODEL at all.
func resolveModel(name string, p model.Provider, endpoint, key string) (string, error) {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name), nil
	}
	if p.Hosted {
		if p.DefaultModel != "" {
			return p.DefaultModel, nil
		}
		return "", fmt.Errorf("model is required for provider %s; set LIKHA_MODEL or --model", p.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ids, err := model.ListModels(ctx, endpoint, key)
	if err != nil {
		return "", fmt.Errorf("model is required; set LIKHA_MODEL or --model (model list failed: %v)", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("model is required; set LIKHA_MODEL or --model (the endpoint at %s reports no models)", endpoint)
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
