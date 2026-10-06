package agent

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"likha/internal/actions"
)

//go:embed init_prompt.md
var initPrompt string

// rootInstructionsPath is the only file an /init turn may propose.
const rootInstructionsPath = "AGENTS.md"

// InitPrompt builds the survey prompt one /init turn sends to the model
// (specs/repo-init). guidance is the optional text after /init; empty means
// the standard survey.
func InitPrompt(guidance string) string {
	prompt := strings.TrimSpace(initPrompt)
	if guidance = strings.TrimSpace(guidance); guidance != "" {
		prompt += "\n\n## User guidance\n\n" + guidance
	}
	return prompt
}

// initEditPath accepts an edit_file path during /init only when it names the
// repository-root AGENTS.md: "AGENTS.md" after cleaning, or an absolute path
// trivially equal to root/AGENTS.md. It returns the repository-relative form.
func initEditPath(root, path string) (string, bool) {
	if filepath.IsAbs(path) {
		if root != "" && filepath.Clean(path) == filepath.Join(root, rootInstructionsPath) {
			return rootInstructionsPath, true
		}
		return "", false
	}
	if isRootInstructionsPath(path) {
		return rootInstructionsPath, true
	}
	return "", false
}

// isRootInstructionsPath reports whether a repository-relative path names the
// root AGENTS.md the harness loads every turn.
func isRootInstructionsPath(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.ToSlash(filepath.Clean(path)) == rootInstructionsPath
}

// rootInstructionsSizeWarning returns the review warning for a root AGENTS.md
// whose proposed content the harness would reject, or "" when it fits.
func rootInstructionsSizeWarning(size int) string {
	if size <= maxRootInstructionsBytes {
		return ""
	}
	return fmt.Sprintf("AGENTS.md is %d bytes; the harness ignores root instructions over 32 KiB (%d bytes), so this file would not be loaded.", size, maxRootInstructionsBytes)
}

// rootInstructionsEditSize computes the root AGENTS.md size after an accepted
// exact-edit batch: a create's content length, or the current size plus each
// replacement's delta (PrepareEdits already proved every old_text matches the
// original exactly once without overlap). It returns 0 when the current size
// cannot be read, which yields no warning.
func rootInstructionsEditSize(root string, ops []actions.EditOperation) int {
	size, replaced := 0, false
	for _, op := range ops {
		if !isRootInstructionsPath(op.Path) {
			continue
		}
		switch op.Kind {
		case "create":
			return len(op.Content)
		case "replace":
			replaced = true
			size += len(op.NewText) - len(op.OldText)
		}
	}
	if !replaced {
		return 0
	}
	info, err := os.Lstat(filepath.Join(root, rootInstructionsPath))
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return int(info.Size()) + size
}
