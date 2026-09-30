package repository

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreeRespectsGitignoreAndNodeModules(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, dirs bool) {
		t.Helper()
		if dirs {
			if err := os.MkdirAll(filepath.Join(root, rel), 0700); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mk("keep.txt", false)
	mk("build/out.bin", false) // root .gitignore: build/
	mk("vendor/lib.js", false) // vendor rule via root gitignore
	mk("node_modules/pkg/index.js", false)
	mk("src/generated.ts", false) // nested .gitignore: generated.ts
	mk("src/hand.ts", false)

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("build/\nvendor/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", ".gitignore"), []byte("generated.ts\n"), 0600); err != nil {
		t.Fatal(err)
	}

	repo, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := repo.Tree(".", 1000)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(entries, "\n")
	for _, forbidden := range []string{"build", "vendor", "node_modules", ".gitignore", "generated.ts"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("entry %q leaked into tree: %v", forbidden, entries)
		}
	}
	for _, want := range []string{"keep.txt", "src/", "src/hand.ts"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in tree: %v", want, entries)
		}
	}

	// Root ignore rules apply when walking a subfolder directly.
	sub, _, err := repo.Tree("src", 1000)
	if err != nil {
		t.Fatal(err)
	}
	subJoined := strings.Join(sub, "\n")
	if strings.Contains(subJoined, "generated.ts") || !strings.Contains(subJoined, "src/hand.ts") {
		t.Fatalf("subfolder walk lost rules: %v", sub)
	}
}

func TestIsDirConfinement(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "dir"), 0700)
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0600)
	repo, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.IsDir("dir"); err != nil || !ok {
		t.Fatalf("IsDir(dir) = %v, %v", ok, err)
	}
	if ok, err := repo.IsDir("file.txt"); err != nil || ok {
		t.Fatalf("IsDir(file) = %v, %v", ok, err)
	}
	if _, err := repo.IsDir("../outside"); err == nil {
		t.Fatal("IsDir accepted a parent traversal")
	}
}
