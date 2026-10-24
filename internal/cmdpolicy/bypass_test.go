package cmdpolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyGoFlagSpellings(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		command string
		tier    Tier
	}{
		// Go's flag package accepts -flag, --flag, -flag=v and --flag=v alike.
		{"gofmt -w x.go", Ask},
		{"gofmt --w x.go", Ask},
		{"gofmt -w=true x.go", Ask},
		{"gofmt --w=true x.go", Ask},
		{"gofmt -cpuprofile=out x.go", Ask},
		{"gofmt --cpuprofile out x.go", Ask},
		{"go env -w GOFLAGS=-toolexec=evil", Ask},
		{"go env --w GOFLAGS=-toolexec=evil", Ask},
		{"go env --w=GOFLAGS=x", Ask},
		{"go env -u GOFLAGS", Ask},
		{"go env --u GOFLAGS", Ask},
		{"go list -toolexec evil ./...", Ask},
		{"go list --toolexec evil ./...", Ask},
		{"go list --toolexec=evil ./...", Ask},
		{"go list --exec=evil ./...", Ask},
		{"go list -debug-trace=trace.json ./...", Ask},
		{"go list --debug-trace trace.json ./...", Ask},
		{"go list -debug-runtime-trace=rt.out ./...", Ask},
		{"go list -debug-actiongraph=graph.json ./...", Ask},
		{"go list --debug-actiongraph=graph.json ./...", Ask},
		{"go list -mod=mod ./...", Ask},
		{"go list --mod=mod ./...", Ask},
		{"go list -mod mod ./...", Ask},
		{"go list -mod", Ask},
		{"go list -modfile=alt.mod ./...", Ask},
		{"go list -export -gcflags=-cpuprofile=cpu.out ./...", Ask},
		{"go list -export --asmflags=-o=x ./...", Ask},
		{"go vet -vettool=./shadow ./...", Ask},
		{"go vet --vettool ./shadow ./...", Ask},

		// Ordinary inspection stays read-only.
		{"gofmt -l .", ReadOnly},
		{"gofmt -d main.go", ReadOnly},
		{"go env GOPATH", ReadOnly},
		{"go env --json", ReadOnly},
		{"go list -m all", ReadOnly},
		{"go list -mod=readonly ./...", ReadOnly},
		{"go list -mod vendor ./...", ReadOnly},
		{"go list -json -deps ./...", ReadOnly},
		{"go vet ./...", Verify},
	}
	for _, tc := range cases {
		if got := Classify(tc.command, root); got.Tier != tc.tier {
			t.Errorf("Classify(%q) = %s (%s), want %s", tc.command, got.Tier, got.Reason, tc.tier)
		}
	}
}

func TestClassifyTailFollowFlags(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		command string
		tier    Tier
	}{
		{"tail -f log.txt", Ask},
		{"tail -F log.txt", Ask},
		{"tail -qf log.txt", Ask},
		{"tail -n5 -f log.txt", Ask},
		{"tail -5f log.txt", Ask},
		{"tail +5f log.txt", Ask},
		{"tail -vF log.txt", Ask},
		{"tail --follow log.txt", Ask},
		{"tail --follow=name log.txt", Ask},
		{"tail --fo log.txt", Ask}, // GNU accepts unambiguous abbreviations
		{"tail --retry log.txt", Ask},
		{"tail --re log.txt", Ask},

		{"tail log.txt", ReadOnly},
		{"tail -n 20 log.txt", ReadOnly},
		{"tail -q -n 5 a.txt b.txt", ReadOnly},
		{"tail --lines=5 log.txt", ReadOnly},
	}
	for _, tc := range cases {
		if got := Classify(tc.command, root); got.Tier != tc.tier {
			t.Errorf("Classify(%q) = %s (%s), want %s", tc.command, got.Tier, got.Reason, tc.tier)
		}
	}
}

func TestClassifyDevices(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		command string
		tier    Tier
		reason  string // a substring of the reason; empty means none
	}{
		// Commands run with stdin on /dev/null and captured output.
		{"cat /dev/null", ReadOnly, "read-only"},
		{"cat /dev/stdin", ReadOnly, "read-only"},
		{"cat /dev/fd/0", ReadOnly, "read-only"},
		{"ls /dev/stdout /dev/stderr /dev/fd/1 /dev/fd/2", ReadOnly, "read-only"},
		{"echo hi > /dev/stderr", Ask, ""},
		{"echo hi >/dev/stdout", Ask, ""},
		{"echo hi 2> /dev/fd/2", Ask, ""},

		// Endless or interactive devices may hang: a prompt, but rememberable.
		{"cat /dev/zero", Ask, "endless or interactive device"},
		{"cat /dev/random", Ask, "endless or interactive device"},
		{"head -c 16 /dev/urandom", Ask, "endless or interactive device"},
		{"cat /dev/tty", Ask, "endless or interactive device"},

		// Other descriptors reach whatever file is open on them.
		{"cat /dev/fd/3", AlwaysAsk, "reaches outside the repository"},
	}
	for _, tc := range cases {
		got := Classify(tc.command, root)
		if got.Tier != tc.tier || tc.reason == "" && got.Reason != "" || !strings.Contains(got.Reason, tc.reason) {
			t.Errorf("Classify(%q) = %s (%q), want %s (%q)", tc.command, got.Tier, got.Reason, tc.tier, tc.reason)
		}
	}
}

func TestClassifyResolvesSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "private")
	if err := os.Mkdir(filepath.Join(outside, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(outside, ".ssh", "id_rsa"), "key")
	writeFile(t, filepath.Join(root, "real.txt"), "fine")
	writeFile(t, filepath.Join(root, ".env"), "TOKEN=x")
	if err := os.MkdirAll(filepath.Join(root, ".venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"notes.txt":        filepath.Join(outside, "secret.txt"),
		"out":              outside,
		"config.txt":       ".env",
		"key":              filepath.Join(outside, ".ssh", "id_rsa"),
		"alias.txt":        "real.txt",
		"inner":            "dir",
		"loop":             "loop",
		"dir/up":           "..",
		".venv/bin/python": filepath.Join(outside, "python3"),
		// Dangling links: writing through one creates its target.
		"new.txt":     filepath.Join(outside, "new.txt"),
		"rel-new.txt": "../" + filepath.Base(outside) + "/rel-new.txt",
		"hop.txt":     "new.txt",
		"future.txt":  "real-future.txt",
		"newkey":      filepath.Join(outside, ".ssh", "id_ed25519"),
	}
	writeFile(t, filepath.Join(outside, "python3"), "#!/bin/sh\n")
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	// A dangling chain longer than any kernel would follow.
	for i := 0; i < 40; i++ {
		if err := os.Symlink(fmt.Sprintf("chain%d", i+1), filepath.Join(root, fmt.Sprintf("chain%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	const (
		outsideLink = "reaches outside the repository through a symlink"
		secretLink  = "touches credentials or secrets"
	)
	cases := []struct {
		command string
		tier    Tier
		reason  string // a substring of the reason; empty means none
	}{
		// A link out of the repository prompts, but may be remembered.
		{"cat notes.txt", Ask, outsideLink},
		{"cat ./notes.txt", Ask, outsideLink},
		{"cat out/secret.txt", Ask, outsideLink},
		{"ls out", Ask, outsideLink},
		{"cat out/missing.txt", Ask, outsideLink},
		{"head --file=notes.txt", Ask, outsideLink},
		{"cat dir/up/notes.txt", Ask, outsideLink},
		{"cat " + root + "/notes.txt", Ask, outsideLink},
		{"echo x > notes.txt", Ask, outsideLink},

		// Dangling links resolve to the target a write would create.
		{"cat new.txt", Ask, outsideLink},
		{"echo x > new.txt", Ask, outsideLink},
		{"cat rel-new.txt", Ask, outsideLink},
		{"cat hop.txt", Ask, outsideLink},
		{"cat future.txt", ReadOnly, "read-only"},
		{"cat chain0", AlwaysAsk, "dangling symlink"},

		// Credentials always ask, wherever the link lands.
		{"cat config.txt", AlwaysAsk, secretLink},
		{"cat key", AlwaysAsk, secretLink},
		{"echo x > newkey", AlwaysAsk, secretLink},
		{"cat loop", AlwaysAsk, "cannot be resolved"},

		// The executable itself may live outside: a virtualenv interpreter.
		{".venv/bin/python -m pytest", Ask, ""},
		{"./.venv/bin/python script.py", Ask, ""},
		{"cat .venv/bin/python", Ask, outsideLink},

		{"cat alias.txt", ReadOnly, "read-only"}, // symlink that stays inside
		{"ls inner", ReadOnly, "read-only"},      // directory symlink that stays inside
		{"cat dir/up/real.txt", ReadOnly, "read-only"},
		{"cat missing.txt", ReadOnly, "read-only"}, // nothing to resolve yet
		{"echo hello", ReadOnly, "read-only"},
	}
	for _, tc := range cases {
		got := Classify(tc.command, root)
		if got.Tier != tc.tier || tc.reason == "" && got.Reason != "" || !strings.Contains(got.Reason, tc.reason) {
			t.Errorf("Classify(%q) = %s (%q), want %s (%q)", tc.command, got.Tier, got.Reason, tc.tier, tc.reason)
		}
	}
}

func TestClassifyRepositoryUnderSecretLookingDirectory(t *testing.T) {
	// Only the repository-relative part of a resolved path counts as secret:
	// a checkout that happens to live under a directory named .aws is not.
	parent := filepath.Join(t.TempDir(), ".aws")
	root := filepath.Join(parent, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "main.go"), "package main\n")
	if got := Classify("cat main.go", root); got.Tier != ReadOnly {
		t.Fatalf("Classify(\"cat main.go\") = %s (%s), want read-only", got.Tier, got.Reason)
	}
}

func TestClassifyScriptReadingThroughEscapingSymlinkAsks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(outside, "data.txt"), "elsewhere")
	if err := os.Symlink(filepath.Join(outside, "data.txt"), filepath.Join(root, "data.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cat data.txt"}}`)
	got := Classify("npm test", root)
	if got.Tier != Ask || !strings.Contains(got.Reason, `the "test" script reaches outside the repository through a symlink`) {
		t.Fatalf("Classify(\"npm test\") = %s (%q), want a rememberable prompt naming the script", got.Tier, got.Reason)
	}
}
