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

	"lisa/internal/model"
	"lisa/internal/repository"
	"lisa/internal/session"
)

const logo = ` _     ___ ____    _
| |   |_ _/ ___|  / \
| |    | |\___ \ / _ \
| |___ | | ___) / ___ \
|_____|___|____/_/   \_\`

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
  --sessions        List saved sessions for the repository; no model needed.
  --resume ID       Resume a saved session for the selected repository.
  --version         Print the build version and exit.
  --help            Print this help and exit.

First-run setup: launching with no provider configured (interactive terminal)
prompts for a provider, your API key, and a model, and stores the choice.

Environment: LISA_MODEL, LISA_ENDPOINT, LISA_PROVIDER, LISA_API_KEY,
LISA_STATE_DIR (private storage directory; default is the OS user
configuration directory's "lisa" child). Each provider also accepts its own
LISA_<NAME>_API_KEY (see the provider list above).

Examples:
  lisa ~/projects/app                                        # first-run setup
  lisa --provider opencode-go ~/projects/app                 # stored key, default model
  lisa --provider openai --api-key sk-... ~/projects/app

Put options before the repository path. Lisa needs an interactive terminal.
`

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
		line := fmt.Sprintf("  %-14s %-52s key: %s", p.Name, p.BaseURL, key)
		if p.DefaultModel != "" {
			line += fmt.Sprintf("  default model: %s", p.DefaultModel)
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	b.WriteString(usageFooter)
	return b.String()
}

// Version is set from the release tag when building distribution binaries.
var Version = "dev"

// Run validates startup settings and runs the interactive terminal UI.
func Run(args []string, stdout, stderr io.Writer) int {
	model.UserAgent = "lisa/" + Version
	flags := flag.NewFlagSet("lisa", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stdout, usageText()) }
	endpoint := flags.String("endpoint", "", "OpenAI-compatible endpoint (overrides the provider default)")
	name := flags.String("model", os.Getenv("LISA_MODEL"), "model name")
	providerName := flags.String("provider", os.Getenv("LISA_PROVIDER"), "provider from the predefined accepted list")
	apiKey := flags.String("api-key", os.Getenv("LISA_API_KEY"), "API key for a hosted provider (stored in the private state directory)")
	listSessions := flags.Bool("sessions", false, "list sessions for the selected repository")
	resumeID := flags.String("resume", "", "resume a previous session ID")
	showVersion := flags.Bool("version", false, "print the build version")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "Lisa %s\n", Version)
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
	stored, err := loadStoredConfig(stateDir)
	if err != nil {
		fmt.Fprintf(stderr, "lisa: %v\n", err)
		return 2
	}
	// Precedence: --provider flag (which includes LISA_PROVIDER) > stored
	// first-run config. A stored model applies only when the provider itself
	// came from storage and no model was given.
	effectiveProvider := *providerName
	if !providerExplicit && *providerName == "" && stored.Provider != "" {
		effectiveProvider = stored.Provider
		if *name == "" && stored.Model != "" {
			*name = stored.Model
		}
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
	var selected model.Provider
	var modelName string
	var client *model.Client
	if !setupNeeded {
		chosen, verified, display, key, err = resolveProvider(effectiveProvider, *endpoint, *apiKey, persistKey, stateDir)
		if err != nil {
			fmt.Fprintf(stderr, "lisa: %v\n", err)
			return 2
		}
		selected, _ = model.LookupProvider(effectiveProvider)
		modelName, err = resolveModel(*name, selected, chosen, key)
		if err != nil {
			fmt.Fprintf(stderr, "lisa: %v\n", err)
			return 2
		}
		client, err = model.New(chosen, modelName, key)
		if err != nil {
			fmt.Fprintf(stderr, "lisa: %v\n", err)
			return 2
		}
	}
	repo, err := repository.New(root)
	if err != nil {
		fmt.Fprintf(stderr, "lisa: %v\n", err)
		return 2
	}
	if !interactive {
		fmt.Fprintln(stderr, "lisa: interactive terminal required")
		return 2
	}
	var startupErr error
	if client != nil {
		// Startup connection check: verify the provider answers before the
		// first prompt. A failure is reported in the interface, not fatal, so
		// the session stays available for another attempt. In setup mode the
		// connection is verified interactively instead.
		startupErr = client.EnsureConnected(context.Background())
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
	program := tea.NewProgram(newUI(root, repo, client, modelName, connection{provider: display, verified: verified, err: startupErr, setup: setupNeeded}, stateDir, store, snapshot), tea.WithAltScreen(), tea.WithInput(os.Stdin), tea.WithOutput(stdout))
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
