package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/mattn/go-isatty"

	"likha/internal/effortprobe"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/tui"
)

// Exit codes of the effort probe (specs/effort-probe § Exit codes).
const (
	probeExitOK          = 0
	probeExitFailed      = 1
	probeExitUsage       = 2
	probeExitInterrupted = 130
)

// probeListTimeout bounds the unbilled model-list GET that may supply live
// levels before consent.
const probeListTimeout = 5 * time.Second

// probeIsTerminal and probeStdin are the consent prompt's seams: the prompt
// is asked only when both stdin and stdout are terminals.
var (
	probeIsTerminal = func(stdout io.Writer) bool {
		out, ok := stdout.(*os.File)
		return ok && isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(out.Fd())
	}
	probeStdin io.Reader = os.Stdin
)

// probeConfig is what the flags give the probe. Provider and model come from
// --provider/--model, whose defaults are LIKHA_PROVIDER/LIKHA_MODEL, and
// never from the stored first-run setup (spec point 1).
type probeConfig struct {
	provider string
	model    string
	endpoint string
	apiKey   string
	wire     string
	levels   string
	yes      bool
}

// probeFlagError enforces spec point 3's flag combinations. It names the
// first conflict, or returns nil.
func probeFlagError(flags *flag.FlagSet, probe, sessions, login bool, resume string) error {
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if !probe {
		for _, name := range []string{"probe-wire", "probe-levels", "yes"} {
			if set[name] {
				return fmt.Errorf("--%s applies only with --probe-effort", name)
			}
		}
		return nil
	}
	switch {
	case sessions || resume != "" || login:
		return errors.New("--probe-effort cannot be combined with --sessions, --resume or --login")
	case flags.NArg() > 0:
		return errors.New("--probe-effort takes no repository path")
	}
	return nil
}

// effortProbeFlow runs `likha --probe-effort` and returns its exit code. It
// stores nothing except the report and, for ChatGPT sign-in, a rotated
// refresh token (spec decision 15).
func effortProbeFlow(cfg probeConfig, stdout, stderr io.Writer) int {
	plan, client, stateDir, code := prepareProbe(cfg, stderr)
	if code != probeExitOK {
		return code
	}
	if err := effortprobe.WritePlan(stdout, plan, effortprobe.Estimate(plan)); err != nil {
		fmt.Fprintf(stderr, "likha: print probe plan: %v\n", err)
		return probeExitFailed
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if proceed, code := confirmProbe(ctx, cfg.yes, plan, stdout, stderr); !proceed {
		return code
	}
	report, err := effortprobe.Run(ctx, client, plan, effortprobe.Progress{W: stderr})
	if err != nil {
		fmt.Fprintf(stderr, "likha: %v\n", err)
		return probeExitFailed
	}
	if err := effortprobe.WriteTable(stdout, report); err != nil {
		fmt.Fprintf(stderr, "likha: print probe results: %v\n", err)
	}
	if report.Sent() > 0 {
		path, err := effortprobe.WriteReport(stateDir, report)
		if err != nil {
			fmt.Fprintf(stderr, "likha: write probe report: %v\n", err)
			return probeExitFailed
		}
		fmt.Fprintf(stdout, "Report: %s\n", path)
	}
	return probeExitCode(report)
}

func probeExitCode(report effortprobe.Report) int {
	switch {
	case report.Interrupted:
		return probeExitInterrupted
	case report.Stop.Failed():
		return probeExitFailed
	}
	return probeExitOK
}

// prepareProbe does everything before the plan is printed: usage checks,
// credentials, the client and the unbilled level lookup. Any failure is a
// usage-class error (exit 2): nothing has been sent.
func prepareProbe(cfg probeConfig, stderr io.Writer) (effortprobe.Plan, *model.Client, string, int) {
	fail := func(format string, args ...any) (effortprobe.Plan, *model.Client, string, int) {
		fmt.Fprintf(stderr, "likha: "+format+"\n", args...)
		return effortprobe.Plan{}, nil, "", probeExitUsage
	}
	providerName, modelID := strings.TrimSpace(cfg.provider), strings.TrimSpace(cfg.model)
	if providerName == "" {
		return fail("--probe-effort needs --provider or LIKHA_PROVIDER; the stored setup provider is never probed, and custom endpoints cannot be probed")
	}
	if modelID == "" {
		return fail("--probe-effort needs --model or LIKHA_MODEL; the provider's default model is never probed by accident")
	}
	selected, ok := model.LookupProvider(providerName)
	if !ok {
		return fail("unknown provider %q (see --help)", providerName)
	}
	if err := effortprobe.CheckWire(selected.Name, cfg.wire); err != nil {
		return fail("%v", err)
	}
	levels, err := parseProbeLevels(cfg.levels)
	if err != nil {
		return fail("%v", err)
	}
	stateDir, err := resolveStateDir(stderr)
	if err != nil {
		return fail("locate state directory: %v", err)
	}
	endpoint := strings.TrimSpace(cfg.endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("LIKHA_ENDPOINT"))
	}
	// persistKey is always false: a probe never stores a passed --api-key.
	res, err := providers.ResolveProvider(selected.Name, endpoint, cfg.apiKey, false, stateDir)
	if err != nil {
		return fail("%v", err)
	}
	// Checked before the client exists, so a refused override never gets
	// the endpoint-override notice about a model list it will not fetch.
	if err := effortprobe.CheckEndpoint(selected, res.Endpoint); err != nil {
		return fail("%v", err)
	}
	client, live, clientErr := probeClient(selected, res, modelID, stateDir, stderr)
	// Collected after discovery, which may have rotated the OAuth tokens.
	secrets, err := probeSecrets(selected.Name, res, stateDir)
	if err != nil {
		return fail("%v", err)
	}
	if clientErr != nil {
		// Discovery errors can carry the provider's response body.
		return fail("%s", effortprobe.Redact(clientErr.Error(), secrets...))
	}
	plan, err := effortprobe.NewPlan(selected, modelID, live, effortprobe.Options{
		Wire: cfg.wire, Levels: levels, Endpoint: res.Endpoint, LikhaVersion: tui.Version,
		Secrets: secrets,
	})
	if err != nil {
		return fail("%v", err)
	}
	return plan, client, stateDir, probeExitOK
}

// probeSecrets lists the credentials the probe must keep out of its output:
// the ones resolved at start plus, for a sign-in, the ones stored now. The
// unbilled discovery request may refresh an expired token, and every probe
// request then sends the rotated one.
func probeSecrets(provider string, res providers.ResolvedProvider, stateDir string) ([]string, error) {
	secrets := []string{res.Key, res.Creds.Access, res.Creds.Refresh, res.Creds.IDToken}
	if !res.OAuth {
		return secrets, nil
	}
	current, _, err := providers.StoredOAuth(stateDir, provider)
	if err != nil {
		return nil, fmt.Errorf("reload %s sign-in after model discovery: %w", provider, err)
	}
	return append(secrets, current.Access, current.Refresh, current.IDToken), nil
}

// parseProbeLevels splits --probe-levels; the probe validates the names.
func parseProbeLevels(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var levels []string
	for _, part := range strings.Split(raw, ",") {
		level := strings.TrimSpace(part)
		if level == "" {
			return nil, fmt.Errorf("--probe-levels %q has an empty level", raw)
		}
		levels = append(levels, level)
	}
	return levels, nil
}

// probeClient builds the client as a normal run does and reads the model's
// live levels from the unbilled model list when the provider publishes them.
// It never contacts an overridden endpoint before consent.
func probeClient(p model.Provider, res providers.ResolvedProvider, modelID, stateDir string, stderr io.Writer) (*model.Client, []string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeListTimeout)
	defer cancel()
	if res.OAuth {
		client, err := model.NewOAuth(res.Endpoint, modelID, res.Creds.Issuer, res.Creds.ClientID, res.Creds)
		if err != nil {
			return nil, nil, err
		}
		bindOAuthStorage(client, stateDir, p.Name)
		_, _, details, err := discoverOAuthModel(ctx, client, modelID)
		if err != nil {
			return nil, nil, err
		}
		return client, liveLevels(details, modelID), nil
	}
	client, err := model.New(res.Endpoint, modelID, res.Key)
	if err != nil {
		return nil, nil, err
	}
	if p.SessionHeader != "" {
		// OpenCode relays route per conversation; a normal run uses its
		// SQLite session ID, which the probe does not open.
		id, err := probeSessionID()
		if err != nil {
			return nil, nil, err
		}
		client.SetSessionHeader(p.SessionHeader)
		client.SetSession(id)
	}
	if effortprobe.EndpointOverridden(p, res.Endpoint) {
		// The override, possibly an ambient LIKHA_ENDPOINT, would receive the
		// key before the plan shows it; the first request waits for consent.
		fmt.Fprintln(stderr, "likha: endpoint override: the model list is not fetched before you confirm; using the bundled catalog's levels")
		return client, nil, nil
	}
	details, err := listModelDetailsFunc(ctx, res.Endpoint, res.Key)
	if err != nil {
		// The error may quote the provider's response, which can echo the key.
		fmt.Fprintf(stderr, "likha: model list unavailable (%s); using the bundled catalog's levels\n", effortprobe.Redact(err.Error(), res.Key))
		return client, nil, nil
	}
	return client, liveLevels(details, modelID), nil
}

func liveLevels(details []model.ModelDetails, modelID string) []string {
	for _, d := range details {
		if d.ID == modelID {
			return d.Efforts
		}
	}
	return nil
}

func probeSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("create probe session ID: %w", err)
	}
	return "likha-probe-" + hex.EncodeToString(b[:]), nil
}

// confirmProbe asks for consent unless --yes was given (spec point 7). It
// reports whether to proceed and, when not, the exit code.
func confirmProbe(ctx context.Context, yes bool, plan effortprobe.Plan, stdout, stderr io.Writer) (bool, int) {
	if yes {
		return true, probeExitOK
	}
	if !probeIsTerminal(stdout) {
		fmt.Fprintln(stderr, "likha: pass --yes to confirm spending; no model request was sent")
		return false, probeExitUsage
	}
	fmt.Fprintf(stderr, "Send %d requests to %s (%s)? [y/N] ", len(plan.Requests()), plan.Provider.Name, plan.Model)
	answers := make(chan string, 1)
	// The read cannot be cancelled; on Ctrl+C the process exits right after,
	// so the goroutine ends with it.
	go func() {
		line, _ := bufio.NewReader(probeStdin).ReadString('\n')
		answers <- line
	}()
	select {
	case <-ctx.Done():
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Probe cancelled; no model request was sent.")
		return false, probeExitInterrupted
	case answer := <-answers:
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			return true, probeExitOK
		}
		fmt.Fprintln(stderr, "Probe cancelled; no model request was sent.")
		return false, probeExitFailed
	}
}
