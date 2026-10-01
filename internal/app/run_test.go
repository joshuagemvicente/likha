package app

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lisa/internal/model"
)

func TestResolveRootFollowsSelectedRepository(t *testing.T) {
	repo := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked-repository")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	root, err := resolveRoot(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if root != want {
		t.Fatalf("selected repository = %q, want %q", root, want)
	}
}

func TestRunRejectsInvalidRepository(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ordinary-file")
	if err := os.WriteFile(file, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"regular file", []string{file}},
		{"missing directory", []string{filepath.Join(t.TempDir(), "absent")}},
		{"extra path", []string{t.TempDir(), t.TempDir()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(tc.args, &stdout, &stderr); code == 0 {
				t.Fatalf("accepted invalid repository: %v", tc.args)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunReportsMissingModel(t *testing.T) {
	t.Setenv("LISA_MODEL", "")
	t.Setenv("LISA_ENDPOINT", "")
	t.Setenv("LISA_STATE_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	// A closed port keeps the test independent of any real model server; with
	// no provider named, the custom endpoint's discovery fails and names the
	// missing configuration.
	if code := Run([]string{"--endpoint", "http://127.0.0.1:9/v1", t.TempDir()}, &stdout, &stderr); code == 0 {
		t.Fatal("accepted missing model configuration")
	}
	if !strings.Contains(stderr.String(), "model is required") || stdout.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunWithoutConfigurationExplainsSetup(t *testing.T) {
	for _, key := range []string{"LISA_MODEL", "LISA_PROVIDER", "LISA_API_KEY", "LISA_ENDPOINT"} {
		t.Setenv(key, "")
	}
	t.Setenv("LISA_STATE_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := Run([]string{t.TempDir()}, &stdout, &stderr); code == 0 {
		t.Fatal("started without provider configuration in a non-interactive terminal")
	}
	if !strings.Contains(stderr.String(), "no provider configured") || stdout.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestResolveModelExplicitBeatsDiscovery(t *testing.T) {
	custom := model.Provider{DisplayName: "Custom endpoint"}
	got, err := resolveModel("  qwen3:4b ", custom, "http://127.0.0.1:9/v1", "")
	if err != nil || got != "qwen3:4b" {
		t.Fatalf("explicit model = %q err=%v", got, err)
	}
}

func TestResolveModelUsesHostedDefault(t *testing.T) {
	p, ok := model.LookupProvider("openrouter")
	if !ok {
		t.Fatal("openrouter missing from table")
	}
	got, err := resolveModel("", p, p.BaseURL, "sk-key")
	if err != nil || got != p.DefaultModel {
		t.Fatalf("hosted default = %q err=%v", got, err)
	}
	hostless := model.Provider{Name: "x", DisplayName: "X", Hosted: true}
	if _, err := resolveModel("", hostless, hostless.BaseURL, "k"); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("hosted without default: err=%v", err)
	}
}

func TestResolveModelDiscoversFirstEndpointModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"smoke-model"},{"id":"other"}]}`)
	}))
	defer server.Close()
	custom := model.Provider{DisplayName: "Custom endpoint"}
	got, err := resolveModel("", custom, server.URL+"/v1", "")
	if err != nil || got != "smoke-model" {
		t.Fatalf("discovered model = %q err=%v", got, err)
	}
}

func TestResolveModelErrorsWithoutEndpointModels(t *testing.T) {
	custom := model.Provider{DisplayName: "Custom endpoint"}
	if _, err := resolveModel("", custom, "http://127.0.0.1:1/v1", ""); err == nil || !strings.Contains(err.Error(), "model list failed") {
		t.Fatalf("unreachable discovery error = %v", err)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[]}`)
	}))
	defer empty.Close()
	if _, err := resolveModel("", custom, empty.URL+"/v1", ""); err == nil || !strings.Contains(err.Error(), "reports no models") {
		t.Fatalf("empty list error = %v", err)
	}
}

func TestRunHelpDoesNotRequireRepository(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help failed: exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: lisa") || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
	for _, expected := range []string{"--provider", "--api-key", "--model", "--endpoint", "--sessions", "--resume", "--version", "LISA_STATE_DIR", "Providers", "BYOK"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("help is missing %q: %q", expected, stdout.String())
		}
	}
}
