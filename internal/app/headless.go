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
	apiKey  string
	stored  providers.StoredProviderConfig
	json    bool
	maxTime time.Duration
}

// headlessFlow checks the connection, applies the stored effort, opens the
// session and runs one headless run (specs/headless-print). MCP servers are
// never started (D7).
func headlessFlow(start headlessStart, stdout, stderr io.Writer) int {
	if start.client == nil {
		fmt.Fprintln(stderr, "likha: no provider configured; set --provider or LIKHA_PROVIDER (see --help)")
		return headless.ExitUsage
	}
	ctx, stop := interruptContext(context.Background())
	defer stop()
	secrets := []string{start.key, strings.TrimSpace(start.apiKey)}
	if start.res.OAuth {
		secrets = append(secrets, start.res.Creds.Access, start.res.Creds.Refresh, start.res.Creds.IDToken)
	}
	fail := func(code int, format string, args ...any) int {
		fmt.Fprintf(stderr, "likha: %s\n", redact.Secrets(fmt.Sprintf(format, args...), secrets...))
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
		live = discoverModelMetadata(modelName, start.res.Endpoint, start.key, start.selected.Name, nil)
	}
	effort, effortNotice := applyStoredEffort(start.client, start.selected, modelName, live.Efforts, start.stored)

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
	if effortNotice != "" {
		notices = append(notices, effortNotice)
	}
	if note := mcpNotice(start.stateDir); note != "" {
		notices = append(notices, note)
	}
	display := start.display
	if display == "" {
		display = start.res.Endpoint
	}
	return headless.Run(ctx, headless.Config{
		Prompt: start.prompt, Root: start.root, Repo: start.repo, Client: start.client,
		Store: start.store, Snapshot: snapshot, StateDir: start.stateDir,
		Provider: start.selected.Name, Display: display, Effort: effort,
		JSON: start.json, MaxTime: start.maxTime, Secrets: secrets, Notices: notices,
	}, stdout, stderr)
}

// applyStoredEffort sends the level stored for this provider and model, as
// an interactive launch does (D6). A stored level the model no longer
// publishes falls back to Default with a notice.
func applyStoredEffort(client *model.Client, p model.Provider, modelID string, live []string, stored providers.StoredProviderConfig) (level, notice string) {
	levels := model.EffortLevels(p, modelID, live)
	if len(levels) == 0 {
		return "", ""
	}
	want := stored.StoredEffort(p.Name, modelID)
	if want == "" {
		return "", ""
	}
	if !slices.Contains(levels, want) {
		return "", "Stored effort " + want + " is not offered for " + modelID + "; using default."
	}
	client.SetEffort(p.Effort, want)
	return want, ""
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

// interruptContext cancels with headless.ErrInterrupted on the first SIGINT
// or SIGTERM and exits at once on the second. stop releases the handler.
func interruptContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
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
			os.Exit(headless.ExitInterrupted)
		case <-done:
		}
	}()
	return ctx, func() {
		signal.Stop(signals)
		close(done)
		cancel(nil)
	}
}

// flagsSet lists every flag given on the command line.
func flagsSet(flags *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}
