package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"likha/internal/headless"
	"likha/internal/mcp"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/redact"
	"likha/internal/repository"
	"likha/internal/session"
)

// maxStdinPrompt bounds a prompt read with -p - (drafting default D1).
const maxStdinPrompt = 256 << 10

// defaultMaxTime bounds a headless run unless --max-time says otherwise.
const defaultMaxTime = 30 * time.Minute

// headlessStdin is where -p - reads its prompt; tests replace it.
var headlessStdin io.Reader = os.Stdin

// headlessFlagError checks the flag combinations a headless run allows
// (drafting default D5). set holds every flag given on the command line.
func headlessFlagError(set map[string]bool) error {
	if !set["p"] {
		for _, name := range []string{"json", "max-time"} {
			if set[name] {
				return fmt.Errorf("--%s applies only with -p", name)
			}
		}
		return nil
	}
	for _, name := range []string{"sessions", "login", "probe-effort"} {
		if set[name] {
			return fmt.Errorf("-p cannot be combined with --%s", name)
		}
	}
	return nil
}

// readPrompt returns the headless prompt: the -p value, or stdin when it is
// "-". Empty prompts and slash commands are usage errors; nothing is sent.
func readPrompt(value string, stdin io.Reader) (string, error) {
	prompt := value
	if value == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, maxStdinPrompt+1))
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		if len(data) > maxStdinPrompt {
			return "", fmt.Errorf("prompt on stdin exceeds %d KiB", maxStdinPrompt>>10)
		}
		if !utf8.Valid(data) {
			return "", errors.New("prompt on stdin is not valid UTF-8")
		}
		prompt = string(data)
	}
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return "", errors.New("-p needs a prompt")
	}
	if strings.HasPrefix(trimmed, "/") {
		return "", errors.New("slash commands are interactive only; a headless prompt cannot start with /")
	}
	return trimmed, nil
}

// headlessStart is what the interactive launch resolved before a headless
// run branches off: the provider, the client and the session store.
type headlessStart struct {
	prompt   string
	root     string
	stateDir string
	repo     *repository.Repository
	store    *session.Store
	resumeID string
	client   *model.Client
	res      providers.ResolvedProvider
	selected model.Provider
	display  string
	key      string
	// apiKey is the --api-key/LIKHA_API_KEY value as given: redacted even
	// when the resolved provider sends no key (a custom endpoint).
	apiKey   string
	json     bool
	maxTime  time.Duration
	variant  *string
	restored *model.Selection
	// windowOverrides are config.json's context_windows; autoCompact is its
	// auto_compact preference. Both apply as in the TUI (D10, FR-42).
	windowOverrides map[string]map[string]int64
	autoCompact     bool
}

// headlessFlow checks the connection, applies the variant, opens the
// session and runs one headless run (specs/headless-print). MCP servers are
// never started (D7).
func headlessFlow(start headlessStart, stdout, stderr io.Writer) int {
	if start.client == nil {
		fmt.Fprintln(stderr, "likha: no provider configured; set --provider or LIKHA_PROVIDER (see --help)")
		return headless.ExitUsage
	}
	ctx, hookCtx, stop := interruptContext(context.Background(), os.Exit)
	defer stop()
	secrets := []string{start.key, strings.TrimSpace(start.apiKey)}
	if start.res.OAuth {
		secrets = append(secrets, start.res.Creds.Access, start.res.Creds.Refresh, start.res.Creds.IDToken)
	}
	fail := func(code int, format string, args ...any) int {
		// A startup step cut short by SIGINT or SIGTERM failed because of
		// the interrupt, so it reports and exits as one.
		if cause := context.Cause(ctx); errors.Is(cause, headless.ErrInterrupted) {
			fmt.Fprintf(stderr, "likha: %v\n", cause)
			return headless.ExitInterrupted
		}
		// The connection check may already have rotated the sign-in's
		// tokens, so the ones it holds now are redacted too.
		current := append(slices.Clone(secrets), start.client.OAuthTokens()...)
		fmt.Fprintf(stderr, "likha: %s\n", redact.Secrets(fmt.Sprintf(format, args...), current...))
		return code
	}

	modelName := start.client.Model()
	var live model.ModelDetails
	if start.res.OAuth {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var details []model.ModelDetails
		var err error
		modelName, _, details, err = discoverOAuthModel(checkCtx, start.client, modelName)
		cancel()
		if err == nil {
			err = start.client.SetModel(modelName)
		}
		if err != nil {
			return fail(headless.ExitFailed, "%s: connection check failed: %v", start.display, err)
		}
		live = modelDetail(details, modelName)
	} else {
		if err := start.client.EnsureConnected(ctx); err != nil {
			return fail(headless.ExitFailed, "%s: connection check failed: %v", start.display, err)
		}
		live = discoverModelMetadata(ctx, modelName, start.res.Endpoint, start.key, start.selected.Name, nil)
	}
	var variantNotice string
	var selectionErr error
	if start.restored != nil {
		variantNotice, selectionErr = providers.ApplySavedVariant(ctx, start.stateDir, start.client, start.selected, live.Reasoning, start.restored.Variant)
	} else {
		variantNotice, selectionErr = providers.ApplyVariant(ctx, start.stateDir, start.client, start.selected, live.Reasoning, start.variant)
	}
	if selectionErr != nil {
		return fail(headless.ExitUsage, "select variant: %v", selectionErr)
	}

	var snapshot session.Snapshot
	var err error
	if start.resumeID != "" {
		snapshot, err = start.store.Load(start.resumeID)
	} else {
		snapshot, err = start.store.Create()
	}
	if err != nil {
		return fail(headless.ExitUsage, "session: %v", err)
	}
	if start.selected.SessionHeader != "" {
		start.client.SetSessionHeader(start.selected.SessionHeader)
		start.client.SetSession(snapshot.ID)
	}

	var notices []string
	if variantNotice != "" {
		notices = append(notices, variantNotice)
	}
	if note := mcpNotice(start.stateDir); note != "" {
		notices = append(notices, note)
	}
	display := start.display
	if display == "" {
		display = start.res.Endpoint
	}
	// Resolved as the TUI does: a config.json override, then the provider's
	// /models metadata, then the bundled catalog. An unknown window (0)
	// never compacts.
	modelName = start.client.Model()
	window, _ := model.ResolveContextWindow(start.selected.Name, modelName, start.windowOverrides[start.selected.Name][modelName], live.ContextWindow)
	return headless.Run(ctx, headless.Config{
		Prompt: start.prompt, Root: start.root, Repo: start.repo, Client: start.client,
		Store: start.store, Snapshot: snapshot, StateDir: start.stateDir,
		Provider: start.selected.Name, Display: display, Variant: start.client.Variant(),
		Created: start.resumeID == "", HookContext: hookCtx,
		JSON: start.json, MaxTime: start.maxTime, Secrets: secrets, Notices: notices,
		ContextWindow: window, AutoCompact: start.autoCompact,
	}, stdout, stderr)
}

// mcpNotice says once that configured MCP servers stay stopped, because MCP
// trust is per session and a headless run can never grant it.
func mcpNotice(stateDir string) string {
	servers, err := mcp.LoadConfig(stateDir)
	if err != nil {
		return "MCP configuration unreadable (" + err.Error() + "); MCP servers are not started in headless runs."
	}
	if len(servers) == 0 {
		return ""
	}
	return fmt.Sprintf("%d configured MCP server(s) are not started in headless runs; MCP tools are unavailable.", len(servers))
}

// forceExitGrace is how long a second interrupt waits, after killing hook
// process groups, for the run to return before exiting anyway.
const forceExitGrace = 2 * time.Second

// interruptContext cancels ctx with headless.ErrInterrupted on the first
// SIGINT or SIGTERM. The second cancels hookCtx, which kills every running
// hook's process group, and calls exit(130) unless the run returns within
// forceExitGrace; a third calls it at once. stop releases the handler.
func interruptContext(parent context.Context, exit func(code int)) (ctx, hookCtx context.Context, stop func()) {
	ctx, cancel := context.WithCancelCause(parent)
	hookCtx, killHooks := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			cancel(headless.ErrInterrupted)
		case <-done:
			return
		}
		select {
		case <-signals:
			killHooks()
		case <-done:
			return
		}
		select {
		case <-time.After(forceExitGrace):
			exit(headless.ExitInterrupted)
		case <-signals:
			exit(headless.ExitInterrupted)
		case <-done:
		}
	}()
	return ctx, hookCtx, func() {
		signal.Stop(signals)
		close(done)
		cancel(nil)
		killHooks()
	}
}

// flagsSet lists every flag given on the command line.
func flagsSet(flags *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}
