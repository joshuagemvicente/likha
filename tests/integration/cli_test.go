package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupWithoutConfigurationExplainsSetup(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/likha", t.TempDir())
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "LIKHA_MODEL=", "LIKHA_PROVIDER=", "LIKHA_API_KEY=", "LIKHA_ENDPOINT=", "LIKHA_STATE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("CLI started without provider configuration: %q", out)
	}
	if !strings.Contains(string(out), "no provider configured") {
		t.Fatalf("CLI did not explain missing provider: %q", out)
	}
}

func TestStartupCommandExplainsMissingModel(t *testing.T) {
	// Point discovery at a closed port so the test does not depend on whether
	// a real model server happens to be running.
	cmd := exec.Command("go", "run", "./cmd/likha", "--endpoint", "http://127.0.0.1:9/v1", t.TempDir())
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "LIKHA_MODEL=", "LIKHA_ENDPOINT=", "LIKHA_STATE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("CLI started without model: %q", out)
	}
	if !strings.Contains(string(out), "model is required") {
		t.Fatalf("CLI did not explain missing model: %q", out)
	}
}

func TestStartupCommandRequiresProviderKey(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/likha", "--provider", "openrouter", "--model", "anthropic/claude-3.5-sonnet", t.TempDir())
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "LIKHA_API_KEY=", "LIKHA_STATE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("CLI started without provider key: %q", out)
	}
	if !strings.Contains(string(out), "requires an API key") {
		t.Fatalf("CLI did not explain missing provider key: %q", out)
	}
}

func TestStartupCommandRejectsUnknownProvider(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/likha", "--provider", "not-a-provider", "--model", "m", t.TempDir())
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "LIKHA_STATE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("CLI started with unknown provider: %q", out)
	}
	if !strings.Contains(string(out), "accepted providers") {
		t.Fatalf("CLI did not list accepted providers: %q", out)
	}
}

func TestChatGPTStartupWithoutAuthDirectsBrowserLogin(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/likha", "--provider", "chatgpt", t.TempDir())
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "LIKHA_MODEL=", "LIKHA_API_KEY=", "LIKHA_ENDPOINT=", "LIKHA_STATE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "--provider chatgpt --login") || strings.Contains(string(out), "--device-login") {
		t.Fatalf("missing ChatGPT login did not direct official browser authorization: %q", out)
	}
}

func TestDeprecatedDeviceLoginRejectedWithoutRepository(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/likha", "--provider", "chatgpt", "--device-login", "/not/a/repository")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "LIKHA_STATE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "no longer supported") || !strings.Contains(string(out), "--provider chatgpt --login") || strings.Contains(string(out), "enter code") {
		t.Fatalf("deprecated device flag attempted a flow instead of actionable rejection: %q", out)
	}
}

func TestBrowserLoginFlagRejectsAPIKeyProvider(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/likha", "--provider", "openai", "--login")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "LIKHA_STATE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "use --provider chatgpt") {
		t.Fatalf("browser login flag did not explain its provider: %q", out)
	}
}

func TestChatGPTCustomEndpointRejectedBeforeAuthorization(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/likha", "--provider", "chatgpt", "--endpoint", "https://example.invalid/v1", "--login")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "LIKHA_ENDPOINT=", "LIKHA_STATE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "official public OpenAI API") {
		t.Fatalf("custom OAuth endpoint was not rejected: %q", out)
	}
}
