package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"likha/internal/cmdpolicy"
	"likha/internal/tools"
)

//go:embed install_prompt.md
var installInstructions string

// InstallPrompt builds the prompt one /install turn sends to the model
// (specs/install-command): the install instructions, an environment snapshot
// Likha detects itself (no shell commands), and the request text after
// /install.
func InstallPrompt(request string) string {
	return installPrompt(request, DetectInstallEnv())
}

// installPrompt is InstallPrompt's pure builder: the instructions, the
// rendered environment, then the trimmed request under "Install request".
func installPrompt(request string, env InstallEnv) string {
	return strings.TrimSpace(installInstructions) + "\n\n" + env.render() + "\n\n## Install request\n\n" + strings.TrimSpace(request)
}

// InstallEnv is the environment snapshot injected into an /install prompt so
// the model need not probe what the gate would refuse before the plan.
type InstallEnv struct {
	OS, Arch string
	// Shell is $SHELL, or "unknown" when unset.
	Shell string
	// StartupFiles are home-relative names ("~/.zshrc") of shell startup
	// files that exist; contents are never read.
	StartupFiles []string
	// PackageManagers are the known package managers found on PATH.
	PackageManagers []string
}

// installStartupFiles and installPackageManagers are the fixed, bounded
// candidate lists DetectInstallEnv checks; nothing is searched recursively.
var (
	installStartupFiles    = []string{".zshrc", ".zprofile", ".zshenv", ".bashrc", ".bash_profile", ".profile", ".config/fish/config.fish"}
	installPackageManagers = []string{"brew", "port", "apt-get", "dnf", "yum", "pacman", "zypper", "apk", "nix", "snap", "winget", "scoop", "choco"}
)

// DetectInstallEnv snapshots the local environment without running commands,
// reading file contents, or touching the network. Any lookup that fails is
// omitted.
func DetectInstallEnv() InstallEnv {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return detectInstallEnv(runtime.GOOS, runtime.GOARCH, home, os.Getenv, exec.LookPath)
}

func detectInstallEnv(goos, goarch, home string, getenv func(string) string, lookPath func(string) (string, error)) InstallEnv {
	env := InstallEnv{OS: goos, Arch: goarch, Shell: strings.TrimSpace(getenv("SHELL"))}
	if env.Shell == "" {
		env.Shell = "unknown"
	}
	if home != "" {
		for _, name := range installStartupFiles {
			if info, err := os.Stat(filepath.Join(home, filepath.FromSlash(name))); err == nil && !info.IsDir() {
				env.StartupFiles = append(env.StartupFiles, "~/"+name)
			}
		}
	}
	for _, name := range installPackageManagers {
		if _, err := lookPath(name); err == nil {
			env.PackageManagers = append(env.PackageManagers, name)
		}
	}
	return env
}

func (env InstallEnv) render() string {
	list := func(items []string) string {
		if len(items) == 0 {
			return "none found"
		}
		return strings.Join(items, ", ")
	}
	return "## Environment (detected by Likha)\n\n" +
		"OS: " + env.OS + "/" + env.Arch + "\n" +
		"Shell: " + env.Shell + "\n" +
		"Startup files: " + list(env.StartupFiles) + "\n" +
		"Package managers on PATH: " + list(env.PackageManagers)
}

// installPlanFirst is the refusal an /install turn returns, before any
// approval flow, for a change attempted before the plan is posted.
func installPlanFirst(tool string) string {
	return "refused (/install): " + tool + " is blocked until the install plan is posted; finish any questions with ask_user, then post the plan with plan_update (every command, every file changed, and your assumptions) before making changes"
}

// installGate tracks one /install turn's plan-first rule. A registry is built
// once per turn, so the gate lives exactly as long as the turn. The flag is
// atomic because parallel-safe reads may run concurrently with it being read;
// plan_update and the gated tools are themselves serial.
type installGate struct {
	posted atomic.Bool
}

// commandAllowed reports whether run_command may proceed to its ordinary
// authorization: always once the plan is posted, and before that only for
// commands in the read-only tier.
func (g *installGate) commandAllowed(command, root string) bool {
	return g.posted.Load() || cmdpolicy.Classify(command, root).Tier == cmdpolicy.ReadOnly
}

// gates reports whether a tool is held back until the plan is posted: every
// built-in write tool. run_command is gated per command by commandAllowed;
// MCP tools keep their ordinary trust-on-first-use flow.
func (g *installGate) gates(tool tools.Tool) bool {
	if tool.Source.Kind == "mcp" {
		return false
	}
	for _, effect := range tool.Effects {
		if effect == tools.Write {
			return true
		}
	}
	return false
}

// wrapAuthorize refuses a gated tool before its own authorizer (and so before
// any approval flow) until the plan is posted.
func (g *installGate) wrapAuthorize(name string, authorize tools.Authorizer) tools.Authorizer {
	return func(ctx context.Context, input json.RawMessage) error {
		if !g.posted.Load() {
			return errors.New(installPlanFirst(name))
		}
		if authorize == nil {
			return nil
		}
		return authorize(ctx, input)
	}
}

// wrapPlanUpdate marks the plan posted after a plan_update call that
// succeeded with at least one step; a refused, failed, or clearing call
// leaves the gate closed.
func (g *installGate) wrapPlanUpdate(run tools.Handler) tools.Handler {
	if run == nil {
		return nil
	}
	return func(ctx context.Context, input json.RawMessage) (tools.Result, error) {
		result, err := run(ctx, input)
		if err == nil && result.Status == tools.Succeeded && planHasSteps(input) {
			g.posted.Store(true)
		}
		return result, err
	}
}

func planHasSteps(input json.RawMessage) bool {
	var args struct {
		Steps []json.RawMessage `json:"steps"`
	}
	return json.Unmarshal(input, &args) == nil && len(args.Steps) > 0
}
