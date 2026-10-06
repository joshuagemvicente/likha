package cmdpolicy

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// refusePatterns match whole-string shapes that are never legitimate for an
// agent to run unattended or after a quick glance at a prompt.
var refusePatterns = []struct {
	pattern *regexp.Regexp
	reason  string
}{
	{regexp.MustCompile(`(?i)\b(curl|wget|fetch)\b[^;&\n]*\|\s*(sudo\s+)?(sh|bash|zsh|dash|ksh|fish|python3?|node|perl|ruby)\b`), "pipes downloaded content into an interpreter"},
	{regexp.MustCompile(`(?i)(^|[\s;&|(])(sh|bash|zsh|dash|ksh|source|\.)\s+<\(\s*(curl|wget|fetch)\b`), "executes downloaded content"},
	{regexp.MustCompile("(?i)\\beval\\b[^;&\\n]*(\\$\\(|`)\\s*(curl|wget|fetch)\\b"), "executes downloaded content"},
	{regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*:\s*\|\s*:`), "is a fork bomb"},
	{regexp.MustCompile(`>\s*/dev/(sd|hd|nvme|disk|rdisk|mmcblk)`), "writes to a raw disk device"},
}

// segmentSeparators end one command and start another, or open a nested
// one (`$( )`, backticks, subshells, braces). Quotes are deliberately not
// honored: text inside quotes is scanned like any other command.
const segmentSeparators = ";&|\n\r()`{}"

// scanDanger returns Refuse or AlwaysAsk with a reason, or Ask when nothing
// dangerous was found (Ask here means "no objection", not "prompt").
func scanDanger(command string, sc scope) (Tier, string) {
	for _, rule := range refusePatterns {
		if rule.pattern.MatchString(command) {
			return Refuse, rule.reason
		}
	}
	command = strings.NewReplacer("${HOME}", "$HOME", "${home}", "$HOME").Replace(command)
	best, reason := Ask, ""
	for _, part := range strings.FieldsFunc(command, func(r rune) bool { return strings.ContainsRune(segmentSeparators, r) }) {
		if tier, why := analyzeSegment(dangerWords(part), sc, 0); tier < best {
			best, reason = tier, why
		}
	}
	return best, reason
}

// dangerWords splits one segment into words with quotes and backslashes
// removed. Redirection operators separate words so `x>/etc/passwd` exposes
// its target.
func dangerWords(segment string) []string {
	fields := strings.FieldsFunc(segment, func(r rune) bool { return unicode.IsSpace(r) || r == '<' || r == '>' })
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		word := strings.Map(func(r rune) rune {
			if r == '"' || r == '\'' || r == '\\' {
				return -1
			}
			return r
		}, field)
		if word != "" && word != "$" {
			words = append(words, word)
		}
	}
	return words
}

const maxNesting = 4

func analyzeSegment(words []string, sc scope, depth int) (Tier, string) {
	best, reason := Ask, ""
	note := func(tier Tier, why string) {
		if tier < best {
			best, reason = tier, why
		}
	}
	for _, word := range words {
		note(pathDanger(word, sc))
	}
	if depth <= maxNesting {
		note(commandDanger(words, sc, depth))
	}
	return best, reason
}

func isAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range word[:eq] {
		if !(r == '_' || unicode.IsLetter(r) || i > 0 && unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

func commandName(word string) string {
	return strings.ToLower(path.Base(word))
}

var wrappers = map[string]bool{
	"command": true, "builtin": true, "exec": true, "nohup": true, "time": true, "noglob": true,
	"stdbuf": true, "caffeinate": true, "unbuffer": true, "env": true, "nice": true, "ionice": true,
	"timeout": true, "gtimeout": true, "xargs": true, "watch": true, "setsid": true, "flock": true,
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true}

func commandDanger(words []string, sc scope, depth int) (Tier, string) {
	if depth > maxNesting {
		return Ask, ""
	}
	i := 0
	for i < len(words) {
		word := words[i]
		name := commandName(word)
		switch {
		case isAssignment(word):
			i++
			continue
		case wrappers[name]:
			// Wrapper options take values (`env -u NAME`, `timeout 30`,
			// `xargs -I X`) whose shapes vary per wrapper. Rather than parse
			// each one, treat every following position as a possible start
			// of the wrapped command and keep the strictest answer; a stray
			// match only costs a prompt.
			best, reason := Ask, ""
			for j := i + 1; j < len(words) && j <= i+8; j++ {
				if tier, why := commandDanger(words[j:], sc, depth+1); tier < best {
					best, reason = tier, why
				}
			}
			return best, reason
		case name == "sudo" || name == "doas" || name == "su" || name == "pkexec" || name == "runas":
			return AlwaysAsk, "runs with elevated privileges (" + name + ")"
		case shells[name]:
			for j := i + 1; j < len(words); j++ {
				if flag := words[j]; strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") && strings.Contains(flag, "c") {
					return analyzeSegment(words[j+1:], sc, depth+1)
				}
			}
			return Ask, ""
		case name == "eval" || name == "source" || name == ".":
			return analyzeSegment(words[i+1:], sc, depth+1)
		}
		break
	}
	if i >= len(words) {
		return Ask, ""
	}
	return programDanger(commandName(words[i]), words[i+1:], sc, depth)
}

var remoteInfra = map[string]bool{
	"kubectl": true, "aws": true, "gcloud": true, "gsutil": true, "az": true, "doctl": true,
	"heroku": true, "fly": true, "flyctl": true, "vercel": true, "netlify": true, "surge": true,
	"eksctl": true, "railway": true, "ansible-playbook": true,
}

var systemControl = map[string]bool{
	"launchctl": true, "systemctl": true, "service": true, "crontab": true, "shutdown": true,
	"reboot": true, "halt": true, "poweroff": true, "scutil": true, "networksetup": true,
	"pmset": true, "csrutil": true, "spctl": true, "nvram": true,
}

func programDanger(name string, args []string, sc scope, depth int) (Tier, string) {
	has := func(values ...string) bool {
		for _, arg := range args {
			for _, value := range values {
				if arg == value {
					return true
				}
			}
		}
		return false
	}
	switch {
	case name == "rm":
		if recursiveFlag(args) {
			for _, target := range args {
				if !strings.HasPrefix(target, "-") && catastrophicTarget(target, sc) {
					return Refuse, "recursively deletes " + target
				}
			}
		}
		return AlwaysAsk, "deletes files (rm)"
	case name == "rmdir" || name == "unlink" || name == "shred" || name == "srm" || name == "trash":
		return AlwaysAsk, "deletes files (" + name + ")"
	case name == "find":
		best, reason := Ask, ""
		for j, arg := range args {
			switch arg {
			case "-delete":
				return AlwaysAsk, "deletes files (find -delete)"
			case "-exec", "-execdir", "-ok", "-okdir":
				if tier, why := analyzeSegment(args[j+1:], sc, depth+1); tier < best {
					best, reason = tier, why
				}
			}
		}
		return best, reason
	case name == "git":
		return gitDanger(args)
	case name == "npm" || name == "pnpm" || name == "yarn" || name == "bun":
		if has("publish", "unpublish", "deprecate", "dist-tag", "owner") {
			return AlwaysAsk, "publishes or changes a published package"
		}
	case name == "cargo" && len(args) > 0 && (args[0] == "publish" || args[0] == "yank" || args[0] == "owner"),
		name == "gem" && len(args) > 0 && (args[0] == "push" || args[0] == "yank" || args[0] == "owner"),
		name == "twine" && has("upload"),
		(name == "poetry" || name == "uv" || name == "hatch" || name == "flit") && has("publish"):
		return AlwaysAsk, "publishes a package"
	case name == "gh":
		if len(args) > 0 {
			switch args[0] {
			case "release", "secret", "variable", "api", "auth", "ruleset":
				return AlwaysAsk, "changes GitHub state (gh " + args[0] + ")"
			case "pr":
				if has("merge", "close") {
					return AlwaysAsk, "merges or closes a pull request"
				}
			case "issue":
				if has("close", "delete", "transfer") {
					return AlwaysAsk, "closes or deletes an issue"
				}
			case "repo":
				if has("delete", "archive", "rename", "edit", "transfer") {
					return AlwaysAsk, "changes a GitHub repository"
				}
			case "workflow", "run":
				if has("run", "disable", "cancel", "rerun", "delete") {
					return AlwaysAsk, "changes GitHub Actions runs"
				}
			}
		}
	case remoteInfra[name]:
		return AlwaysAsk, "operates on remote infrastructure (" + name + ")"
	case name == "terraform" || name == "tofu" || name == "pulumi" || name == "helm" || name == "wrangler" || name == "firebase":
		if has("apply", "destroy", "import", "taint", "up", "install", "upgrade", "uninstall", "delete", "rollback", "deploy", "publish", "secret", "refresh") {
			return AlwaysAsk, "deploys or changes infrastructure (" + name + ")"
		}
	case name == "docker" || name == "podman":
		if has("push") {
			return AlwaysAsk, "pushes a container image"
		}
		if has("rm", "rmi", "kill", "prune") {
			return AlwaysAsk, "deletes containers, images, or volumes"
		}
	case name == "chown" || name == "chgrp":
		return AlwaysAsk, "changes file ownership"
	case name == "chmod":
		if recursiveFlag(args) {
			return AlwaysAsk, "recursively changes permissions"
		}
	case name == "kill" || name == "pkill" || name == "killall":
		return AlwaysAsk, "stops processes (" + name + ")"
	case systemControl[name], name == "defaults" && has("write", "delete"):
		return AlwaysAsk, "changes system configuration (" + name + ")"
	case strings.HasPrefix(name, "mkfs") || strings.HasPrefix(name, "newfs"):
		return Refuse, "formats a filesystem"
	case name == "diskutil":
		for _, arg := range args {
			lower := strings.ToLower(arg)
			if strings.HasPrefix(lower, "erase") || strings.HasPrefix(lower, "partition") || strings.HasPrefix(lower, "zero") || strings.HasPrefix(lower, "secureerase") || strings.HasPrefix(lower, "reformat") {
				return Refuse, "erases or repartitions a disk"
			}
		}
	case name == "fdisk" || name == "parted" || name == "gdisk" || name == "sfdisk":
		return AlwaysAsk, "edits disk partitions"
	case name == "dd":
		for _, arg := range args {
			if strings.HasPrefix(arg, "of=/dev/") {
				return Refuse, "writes directly to a device"
			}
		}
	}
	return Ask, ""
}

// recursiveFlag reports -r/-R in any short-flag cluster, or --recursive.
func recursiveFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--recursive" {
			return true
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.ContainsAny(arg, "rR") {
			return true
		}
	}
	return false
}

func catastrophicTarget(target string, sc scope) bool {
	switch strings.TrimRight(target, "/") {
	case "", "/*", "~", "~/*", "$HOME", "$HOME/*", "..", "../*", ".", "./*", "*", ".*":
		return true
	}
	if strings.HasPrefix(target, "/") {
		return sc.isRoot(target)
	}
	return false
}

func gitDanger(args []string) (Tier, string) {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		switch args[i] {
		case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path":
			i++
		}
		i++
	}
	if i >= len(args) {
		return Ask, ""
	}
	sub, rest := args[i], args[i+1:]
	has := func(values ...string) bool {
		for _, arg := range rest {
			for _, value := range values {
				if arg == value {
					return true
				}
			}
		}
		return false
	}
	for _, arg := range rest {
		if strings.HasPrefix(arg, "--force") {
			return AlwaysAsk, "forces a git operation (git " + sub + " " + arg + ")"
		}
	}
	switch sub {
	case "push":
		return AlwaysAsk, "pushes to a remote (git push)"
	case "clean":
		return AlwaysAsk, "deletes untracked files (git clean)"
	case "rm":
		return AlwaysAsk, "deletes files (git rm)"
	case "reset":
		if has("--hard", "--merge", "--keep") {
			return AlwaysAsk, "discards uncommitted changes (git reset --hard)"
		}
	case "rebase":
		return AlwaysAsk, "rewrites commit history (git rebase)"
	case "restore":
		return AlwaysAsk, "discards working-tree changes (git restore)"
	case "checkout":
		if has("--", ".", "-f", "-B") {
			return AlwaysAsk, "discards working-tree changes (git checkout)"
		}
	case "switch":
		if has("-f", "--discard-changes", "-C") {
			return AlwaysAsk, "discards working-tree changes (git switch)"
		}
	case "branch":
		if has("-d", "-D", "--delete", "-m", "-M", "--move", "-f") {
			return AlwaysAsk, "deletes or renames branches"
		}
	case "tag":
		if has("-d", "--delete", "-f") {
			return AlwaysAsk, "deletes or moves tags"
		}
	case "stash":
		if has("drop", "clear") {
			return AlwaysAsk, "deletes stashed changes"
		}
	case "commit":
		if has("--amend") {
			return AlwaysAsk, "rewrites the last commit (git commit --amend)"
		}
	case "filter-branch", "filter-repo", "update-ref", "replace":
		return AlwaysAsk, "rewrites repository history (git " + sub + ")"
	case "reflog":
		if has("expire", "delete") {
			return AlwaysAsk, "deletes reflog entries"
		}
	case "gc", "prune":
		return AlwaysAsk, "prunes repository objects (git " + sub + ")"
	case "worktree":
		if has("remove", "prune") {
			return AlwaysAsk, "deletes a worktree"
		}
	case "config":
		if has("--global", "--system") {
			return AlwaysAsk, "changes global git configuration"
		}
	}
	return Ask, ""
}

// pathDanger flags credentials and anything outside the repository. Flag
// values (`--out=/x`) and assignment values (`DIR=~/x`) are checked too.
func pathDanger(word string, sc scope) (Tier, string) {
	value := word
	if strings.HasPrefix(value, "-") || isAssignment(value) {
		eq := strings.IndexByte(value, '=')
		if eq < 0 {
			return Ask, ""
		}
		value = value[eq+1:]
	}
	if value == "" {
		return Ask, ""
	}
	if secretPath(value) {
		return AlwaysAsk, "touches credentials or secrets (" + value + ")"
	}
	if outsidePath(value, sc) {
		return AlwaysAsk, "reaches outside the repository (" + value + ")"
	}
	return Ask, ""
}

var safeDevices = map[string]bool{"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true, "/dev/stdin": true, "/dev/tty": true, "/dev/zero": true}

func outsidePath(value string, sc scope) bool {
	if strings.HasPrefix(value, "~") || strings.Contains(value, "$HOME") {
		return true
	}
	if strings.HasPrefix(value, "/") {
		clean := filepath.Clean(value)
		if safeDevices[clean] || strings.HasPrefix(clean, "/dev/fd/") {
			return false
		}
		return !sc.inside(clean)
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	return clean == ".." || strings.HasPrefix(clean, "../")
}

var secretDirs = map[string]bool{".ssh": true, ".aws": true, ".gnupg": true, ".kube": true}

var secretFiles = map[string]bool{".netrc": true, ".npmrc": true, ".pypirc": true, ".envrc": true, "providers.json": true, "tool-keys.json": true}

var exampleEnvFiles = map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true, ".env.dist": true}

func secretPath(value string) bool {
	elements := strings.Split(filepath.ToSlash(value), "/")
	for _, element := range elements {
		if secretDirs[strings.ToLower(element)] {
			return true
		}
	}
	base := strings.ToLower(elements[len(elements)-1])
	switch {
	case secretFiles[base]:
		return true
	case base == ".env", strings.HasPrefix(base, ".env.") && !exampleEnvFiles[base]:
		return true
	case strings.HasPrefix(base, "id_rsa"), strings.HasPrefix(base, "id_ed25519"), strings.HasPrefix(base, "id_ecdsa"), strings.HasPrefix(base, "id_dsa"):
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".keystore"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}
