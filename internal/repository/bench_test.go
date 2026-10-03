package repository

import (
	"os"
	"path/filepath"
	"testing"
)

// benchRepo points a Repository at a synthetic tree with the shape of a real
// project: nested source files, generated output and a dependency tree.
func benchRepo(b *testing.B) (*Repository, string) {
	b.Helper()
	root := b.TempDir()
	dirs := []string{"internal/app", "internal/model", "internal/repository", "cmd/likha", "docs", "testdata/fixtures"}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			b.Fatal(err)
		}
	}
	body := make([]byte, 4096)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	for _, dir := range dirs {
		for i := range 60 {
			name := filepath.Join(dir, "file"+itoa(i)+".go")
			if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	// node_modules mirrors the excluded trees real repositories carry.
	for i := range 50 {
		if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkg"+itoa(i), "lib"), 0o755); err != nil {
			b.Fatal(err)
		}
		for j := range 20 {
			name := filepath.Join("node_modules", "pkg"+itoa(i), "lib", "m"+itoa(j)+".js")
			if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	repo, err := New(root)
	if err != nil {
		b.Fatal(err)
	}
	return repo, root
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [8]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}

// BenchmarkGlob measures the whole-tree walk behind the glob tool.
func BenchmarkGlob(b *testing.B) {
	repo, _ := benchRepo(b)
	b.ResetTimer()
	for range b.N {
		if _, err := repo.Glob("**/*.go"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGrep measures the whole-tree scan behind the grep tool.
func BenchmarkGrep(b *testing.B) {
	repo, _ := benchRepo(b)
	b.ResetTimer()
	for range b.N {
		if _, err := repo.Grep("qxz"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTree measures the walk behind the @ popup index and folder
// references.
func BenchmarkTree(b *testing.B) {
	repo, _ := benchRepo(b)
	b.ResetTimer()
	for range b.N {
		if _, _, err := repo.Tree(".", 2000); err != nil {
			b.Fatal(err)
		}
	}
}
