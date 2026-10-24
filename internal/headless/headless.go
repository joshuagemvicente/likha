// Package headless runs one Likha prompt without a terminal UI
// (specs/headless-print, FR-44). Nothing prompts: every action that would
// open a review or a question is refused and reported, never approved.
package headless

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"likha/internal/agent"
	"likha/internal/hooks"
	"likha/internal/model"
	"likha/internal/redact"
	"likha/internal/repository"
	"likha/internal/runsetup"
	"likha/internal/session"
	"likha/internal/tooloutput"
	"likha/internal/tools"
)

// Exit codes (spec drafting default D2).
const (
	ExitCompleted   = 0
	ExitFailed      = 1
	ExitUsage       = 2
	ExitInterrupted = 130
)

// NoReviewerReason is the refusal reason every review gets in a headless run.
const NoReviewerReason = "no reviewer is present in headless mode"

// ErrInterrupted is the cancellation cause a caller sets when SIGINT or
// SIGTERM stops the run; Run then reports cancelled and exits 130.
var ErrInterrupted = errors.New("interrupted by signal")

// errTimeLimit is the cancellation cause when --max-time elapses.
var errTimeLimit = errors.New("time limit reached")

// errOutputFailed is the cancellation cause when the run's output could not
// be written: nobody is reading it, so the run stops instead of spending
// requests and running tools for no one (exit 1, output not written).
var errOutputFailed = errors.New("output not written")

// Run statuses reported in the summary and the result event.
const (
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusCancelled = "cancelled"
	statusTimeLimit = "time_limit"
)

// Config is one headless run. Client, Repo and Store must be ready: the
// caller has resolved the provider, checked the connection and created or
// loaded Snapshot.
type Config struct {
	Prompt   string
	Root     string
	Repo     *repository.Repository
	Client   *model.Client
	Store    *session.Store
	Snapshot session.Snapshot
	StateDir string
	// Provider is the canonical provider ID used for pricing; Display is
	// its human name for messages.
	Provider, Display string
	Variant           string
	// Created means Snapshot was created for this run rather than resumed,
	// so a prompt a hook blocks removes it again: a blocked prompt saves
	// nothing.
	Created bool
	// HookContext parents every observe-only hook process; nil means
	// context.Background(). Hooks deliberately outlive the run's context so
	// run_end still fires after a cancel; cancelling HookContext (a second
	// interrupt) kills their process groups instead.
	HookContext context.Context
	// JSON selects JSON Lines on stdout instead of text.
	JSON bool
	// MaxTime bounds the whole run; zero means no bound.
	MaxTime time.Duration
	// ContextWindow is the model's resolved context window, 0 when unknown;
	// AutoCompact is config.json's auto_compact preference. Together they
	// enable automatic compaction (D10, FR-42) as in the TUI.
	ContextWindow int64
	AutoCompact   bool
	// Secrets are removed from every message Likha writes, and so is every
	// token Client's sign-in has held during the run (see secrets).
	Secrets []string
	// Notices are startup notes reported before the run starts.
	Notices []string

	// resumedPlan is the stored plan as resumeState loaded it, nil when
	// there was none; save compares the row with it to see whether the run
	// rewrote the plan.
	resumedPlan *session.Plan
}

// Run executes one run and returns its exit code. ctx cancellation with
// cause ErrInterrupted reports the run as interrupted.
func Run(ctx context.Context, cfg Config, stdout, stderr io.Writer) int {
	// A consumer that stops reading (likha -p … | head -1) must not kill the
	// run before its session is saved. With SIGPIPE caught, a write to the
	// closed pipe fails with EPIPE instead, which the renderer reports as
	// output not written (exit 1).
	brokenPipe := make(chan os.Signal, 1)
	signal.Notify(brokenPipe, syscall.SIGPIPE)
	defer signal.Stop(brokenPipe)
	client, err := cfg.Client.Fork()
	if err != nil {
		fmt.Fprintf(stderr, "likha: capture execution selection: %v\n", err)
		return ExitUsage
	}
	cfg.Client = client
	cfg.Variant = client.Variant()
	selection := model.Selection{Provider: cfg.Provider, Model: client.Model(), Variant: cfg.Variant}
	cfg.Snapshot.Selection = &selection
	if cfg.MaxTime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, cfg.MaxTime, errTimeLimit)
		defer cancel()
	}
	ctx, stopRun := context.WithCancelCause(ctx)
	defer stopRun(nil)
	// Reconcile before the recorder copies the snapshot, as the TUI does
	// when it resumes a session. A session whose records cannot be read
	// could not be saved either, so the run stops before any request.
	if err := resumeState(&cfg); err != nil {
		fmt.Fprintf(stderr, "likha: session %s cannot be resumed: %s; no request was sent\n", cfg.Snapshot.ID, cfg.clean(err.Error()))
		return ExitUsage
	}
	out := newRenderer(cfg, stdout, stderr)
	options, notices := runOptions(cfg)
	resumed := len(cfg.Snapshot.History) > 0
	rec := newRecorder(cfg)
	out.start(startInfo{
		SessionID: cfg.Snapshot.ID, Repository: cfg.Root, Provider: cfg.Display,
		Model: cfg.Client.Model(), Effort: effortName(cfg.Client.Effort()), Variant: effortName(cfg.Variant),
	})
	runner, hookNotice := loadHooks(cfg.StateDir)
	options.Hooks = runner
	if hookNotice != "" {
		notices = append(notices, hookNotice)
	}
	for _, notice := range append(append([]string(nil), cfg.Notices...), notices...) {
		out.event(agent.TurnEvent{Kind: "notice", Text: notice})
		rec.observe(agent.TurnEvent{Kind: "notice", Text: notice})
	}

	// Parallel read-only tool groups and background hooks emit from several
	// goroutines. deliver returns the renderer's first write error.
	var mu sync.Mutex
	deliver := func(ev agent.TurnEvent) error {
		mu.Lock()
		defer mu.Unlock()
		rec.observe(ev)
		out.event(ev)
		return out.writeErr()
	}
	hookCtx := cfg.HookContext
	if hookCtx == nil {
		hookCtx = context.Background()
	}
	// A failed write while reporting a hook warning is reported by finish.
	// Only the agent's own events stop the run, so a prompt_submit hook
	// never sees a context cancelled before the run began.
	hookRun := &hookSet{ctx: hookCtx, runner: runner, sessionID: cfg.Snapshot.ID, root: cfg.Root, warn: func(text string) {
		_ = deliver(agent.TurnEvent{Kind: "notice", Text: text})
	}}
	hookRun.sessionStart(resumed)
	if block := hookRun.promptBlocked(ctx, cfg.Prompt); block != nil {
		// Nothing was sent or saved; the prompt is the user's to change.
		hookRun.wait()
		return finishBlocked(cfg, out, stderr, block)
	}
	// A run cancelled while its prompt_submit hook ran began no work: like
	// the TUI, it records no marker and cuts no plan step short.
	began := ctx.Err() == nil
	token := session.NewRunToken()
	if began {
		if err := markRunStarted(cfg, token); err != nil {
			_ = deliver(agent.TurnEvent{Kind: "notice", Text: cfg.clean(err.Error()) + "; if this run is cut short, resuming will not report its plan steps as interrupted."})
		}
	}
	agent.RunTurnWithOptions(ctx, cfg.Client, cfg.Repo, cfg.Root, cfg.Snapshot.History, cfg.Prompt, nil, nil, options, func(ev agent.TurnEvent) {
		if hookRun.turnEvent(ev) {
			return
		}
		if err := deliver(ev); err != nil {
			stopRun(errOutputFailed)
		}
	})

	// Background hooks may still deliver warnings into rec, so read and save
	// it under the same lock.
	mu.Lock()
	result := rec.result(ctx)
	if notice, stopped := rec.stopNotice(ctx); stopped {
		rec.markStopped(notice)
	}
	rec.endTurn()
	// Save before waiting for run_end hooks: they may run for their whole
	// timeout, and a second interrupt during that wait must not lose the
	// session. The hooks therefore see the run's outcome, not the save's.
	// A run that began and did not complete cut its active plan steps short.
	interrupted := began && result.Status != statusCompleted
	saveErr := save(cfg, rec, resumed, interrupted)
	saved := len(rec.entries)
	mu.Unlock()
	// Cleared as soon as the run is saved: a run cut short saved its plan
	// interrupted, so only a run that dies before its save leaves a marker,
	// and being killed while the run_end hooks run must not.
	if saveErr == nil && began {
		clearRunMarker(cfg, token, stderr)
	}
	hookRun.runEnd(result)
	hookRun.wait()
	if saveErr != nil {
		result.markSaveFailed(saveErr)
	} else {
		saveLateWarnings(cfg, rec, &mu, saved, resumed, interrupted, stderr)
	}
	if err := out.finish(result); err != nil {
		fmt.Fprintf(stderr, "likha: write output: %v\n", err)
		return ExitFailed
	}
	return result.exitCode()
}

// resumeState reconciles a resumed session as the TUI does on resume:
// explore tasks an earlier run left running are marked interrupted
// (session.ResumeTasks), and plan steps a run that never completed left
// active are reported interrupted (session.ResumePlan). Nothing restarts; the
// run's save keeps the result.
func resumeState(cfg *Config) error {
	if cfg.Created || cfg.Store == nil || cfg.Snapshot.ID == "" {
		return nil
	}
	records, err := cfg.Store.Tasks(cfg.Snapshot.ID)
	if err != nil {
		return fmt.Errorf("load explore records: %w", err)
	}
	session.ResumeTasks(&cfg.Snapshot, records)
	plan, found, err := cfg.Store.ResumablePlan(cfg.Snapshot.ID)
	if err != nil {
		return fmt.Errorf("load plan: %w", err)
	}
	if !found {
		session.ResumePlan(&cfg.Snapshot, nil)
		return nil
	}
	cfg.resumedPlan = &plan
	session.ResumePlan(&cfg.Snapshot, &plan)
	return nil
}

// markRunStarted records that the run began working in its session, so a
// run cut short leaves its plan steps reported as interrupted on resume.
func markRunStarted(cfg Config, token session.RunToken) error {
	if cfg.Store == nil || cfg.Snapshot.ID == "" {
		return nil
	}
	if err := cfg.Store.MarkRunStarted(cfg.Snapshot.ID, token); err != nil {
		return fmt.Errorf("run start not recorded: %w", err)
	}
	return nil
}

// clearRunMarker clears the run marker once the run's session is saved. The
// session is already saved, so a failure is reported but does not fail the
// run.
func clearRunMarker(cfg Config, token session.RunToken, stderr io.Writer) {
	if cfg.Store == nil || cfg.Snapshot.ID == "" {
		return
	}
	if err := cfg.Store.MarkRunCompleted(cfg.Snapshot.ID, token); err != nil {
		fmt.Fprintf(stderr, "likha: session %s: run end not recorded (%s); resuming it will report in-progress plan steps as interrupted\n", cfg.Snapshot.ID, cfg.clean(err.Error()))
	}
}

// saveLateWarnings saves the session again when hooks that outlived the
// first save (run_end, or a late post_tool) added warnings to it, so the
// stored transcript keeps them as the TUI does. The run is already saved, so
// a failure here is reported but does not fail the run.
func saveLateWarnings(cfg Config, rec *recorder, mu *sync.Mutex, saved int, resumed, interrupted bool, stderr io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	if len(rec.entries) == saved {
		return
	}
	if err := save(cfg, rec, resumed, interrupted); err != nil {
		fmt.Fprintf(stderr, "likha: session %s saved without its late hook warnings: %s\n", cfg.Snapshot.ID, cfg.clean(err.Error()))
	}
}

// finishBlocked reports a run whose prompt a prompt_submit hook blocked: no
// request was sent and nothing was saved, and the run exits 1 (failed).
func finishBlocked(cfg Config, out renderer, stderr io.Writer, block *hooks.BlockError) int {
	result := runResult{SessionID: cfg.Snapshot.ID, Status: statusFailed, Err: cfg.clean(block.PromptMessage()), Totals: totals{CostKnown: true}}
	if cfg.Created {
		if err := discardSession(cfg); err != nil {
			result.Err += "; " + cfg.clean(err.Error())
		}
	}
	if err := out.finish(result); err != nil {
		fmt.Fprintf(stderr, "likha: write output: %v\n", err)
	}
	return ExitFailed
}

// discardSession removes the session created for a run whose prompt was
// blocked, with the tool-output directory opened for it, so the block leaves
// no empty session behind.
func discardSession(cfg Config) error {
	if err := cfg.Store.Delete(cfg.Snapshot.ID); err != nil {
		return fmt.Errorf("discard blocked session %s: %w", cfg.Snapshot.ID, err)
	}
	if cfg.StateDir == "" {
		return nil
	}
	if err := tooloutput.RemoveSession(cfg.StateDir, cfg.Snapshot.ID); err != nil {
		return fmt.Errorf("discard blocked session %s tool output: %w", cfg.Snapshot.ID, err)
	}
	return nil
}

// runOptions builds the agent options for a run nobody can review: no ask
// broker, no web consent, no steering, no grants, and NoReviewer set. It
// returns notices for profiles held out of the run.
func runOptions(cfg Config) (agent.RunOptions, []string) {
	options := agent.RunOptions{
		SessionID:  cfg.Snapshot.ID,
		Provider:   cfg.Provider,
		NoReviewer: NoReviewerReason,
		// The same window and config.json preference the TUI passes.
		ContextWindow: cfg.ContextWindow,
		AutoCompact:   cfg.AutoCompact,
	}
	var notices []string
	if cfg.StateDir != "" && cfg.Snapshot.ID != "" {
		outputs, err := tooloutput.Open(cfg.StateDir, cfg.Snapshot.ID)
		if err != nil {
			notices = append(notices, "Private tool output unavailable: "+err.Error())
		} else {
			options.Outputs = outputs
		}
	}
	if cfg.Store != nil && cfg.Snapshot.ID != "" {
		options.TasksEnabled = cfg.Client != nil && cfg.Repo != nil
		runsetup.ApplySession(&options, cfg.Store, cfg.Snapshot.ID)
	}
	options.SkillAdvert, options.SkillLoad = runsetup.SkillCatalog(cfg.StateDir)
	profiles := runsetup.ProfileInputs(cfg.StateDir, cfg.Client)
	options.TaskAgents, options.TaskProfiles = profiles.Agents, profiles.Profiles
	if len(profiles.Catalog.Errors) > 0 {
		notices = append(notices, "Profile catalog: "+strings.Join(profiles.Catalog.Errors, "; "))
	}
	for _, skip := range profiles.Skipped {
		if skip.Err != nil {
			notices = append(notices, "Profile "+skip.Profile.Name+" refused: "+skip.Err.Error())
			continue
		}
		notices = append(notices, fmt.Sprintf("Profile %s requires model %s; the active provider is serving %s, so it is not advertised this run.", skip.Profile.Name, skip.Profile.Model, skip.Serving))
	}
	runsetup.ApplyCommandTrust(&options, cfg.StateDir, cfg.Root)
	// A headless run can never record trust; the hook only fires after a
	// reviewed approval, which cannot happen here.
	options.TrustChecks = nil
	return options, notices
}

func effortName(level string) string {
	if level == "" {
		return "default"
	}
	return level
}

// bound cuts text to at most limit bytes on a character boundary.
func bound(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// clean removes credentials and keeps text valid UTF-8.
func (c Config) clean(text string) string {
	return redact.Secrets(strings.ToValidUTF8(text, "�"), c.secrets()...)
}

// secrets lists everything output must not show: Secrets, and every token
// the client's sign-in has held so far, since a refresh can rotate them in
// the middle of a run. The list only grows during a run.
func (c Config) secrets() []string {
	tokens := c.Client.OAuthTokens()
	if len(tokens) == 0 {
		return c.Secrets
	}
	return append(slices.Clone(c.Secrets), tokens...)
}

// isRefusal reports whether a tool result was refused rather than run.
func isRefusal(result *tools.Result) bool {
	return result != nil && result.Status == tools.Refused
}
