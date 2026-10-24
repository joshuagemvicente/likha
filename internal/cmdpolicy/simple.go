package cmdpolicy

import "strings"

// simpleWords lexes a command that may run without a prompt. It accepts
// only plain words and literal quoting: any operator, redirection,
// expansion, substitution, glob, escape, or comment makes the command not
// simple, and it falls back to a prompt.
func simpleWords(command string) ([]string, bool) {
	var words []string
	var current strings.Builder
	inWord := false
	var quote rune
	for _, r := range command {
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '$', '`', '\\':
				return nil, false
			default:
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		case strings.ContainsRune(";&|<>$`(){}\\*?[]\n\r", r):
			return nil, false
		case !inWord && (r == '~' || r == '#' || r == '!'):
			return nil, false
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	if inWord {
		words = append(words, current.String())
	}
	return words, true
}

func hasAny(args []string, banned ...string) bool {
	for _, arg := range args {
		for _, value := range banned {
			if arg == value || strings.HasPrefix(value, "-") && strings.HasPrefix(arg, value+"=") {
				return true
			}
		}
	}
	return false
}

// hasGoFlag reports whether args set any of the named flags of a Go tool.
// Go's flag package reads -name, --name, -name=v and --name=v as one flag.
func hasGoFlag(args []string, names ...string) bool {
	for _, arg := range args {
		flag, ok := strings.CutPrefix(arg, "-")
		if !ok {
			continue
		}
		flag, _, _ = strings.Cut(strings.TrimPrefix(flag, "-"), "=")
		for _, name := range names {
			if flag == name {
				return true
			}
		}
	}
	return false
}

// tailFollows reports whether tail would keep running instead of exiting:
// f or F anywhere in a short-flag group (-qf, and the obsolete -5f and +5f
// forms), or a long option GNU tail expands to --follow or --retry (it
// accepts any unambiguous abbreviation, such as --fo).
func tailFollows(args []string) bool {
	for _, arg := range args {
		if long, ok := strings.CutPrefix(arg, "--"); ok {
			name, _, _ := strings.Cut(long, "=")
			if name != "" && (strings.HasPrefix("follow", name) || strings.HasPrefix("retry", name)) {
				return true
			}
			continue
		}
		if (strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "+")) && strings.ContainsAny(arg[1:], "fF") {
			return true
		}
	}
	return false
}

func hasPrefix(args []string, prefixes ...string) bool {
	for _, arg := range args {
		for _, prefix := range prefixes {
			if strings.HasPrefix(arg, prefix) {
				return true
			}
		}
	}
	return false
}

var plainReadOnly = map[string]bool{
	"ls": true, "pwd": true, "cat": true, "head": true, "wc": true, "echo": true, "which": true,
	"file": true, "stat": true, "du": true, "tree": true, "basename": true, "dirname": true,
	"realpath": true, "uname": true, "whoami": true, "true": true,
}

var versionTools = map[string]bool{
	"node": true, "npm": true, "pnpm": true, "yarn": true, "bun": true, "deno": true, "go": true,
	"cargo": true, "rustc": true, "python": true, "python3": true, "pip": true, "pip3": true,
	"git": true, "make": true, "gcc": true, "clang": true, "java": true, "ruby": true, "gh": true,
	"docker": true, "tsc": true, "uv": true, "ruff": true, "mypy": true, "pytest": true,
}

// readOnly accepts inspection commands that cannot change state. Paths
// outside the repository and secrets were already escalated by scanDanger.
func readOnly(words []string) bool {
	name, args := words[0], words[1:]
	if strings.Contains(name, "/") {
		return false
	}
	if len(args) == 1 && versionTools[name] && (args[0] == "--version" || args[0] == "-v" && isNodeTool(name) || args[0] == "version" && (name == "go" || name == "docker")) {
		return true
	}
	switch {
	case plainReadOnly[name]:
		return true
	case name == "tail":
		return !tailFollows(args)
	case name == "gofmt":
		// -w rewrites files; -cpuprofile writes a profile to any path.
		return !hasGoFlag(args, "w", "cpuprofile")
	case name == "go" && len(args) > 0:
		switch args[0] {
		case "env":
			return !hasGoFlag(args[1:], "w", "u")
		case "list":
			return goListReadOnly(args[1:])
		}
	case name == "git" && len(args) > 0:
		return gitReadOnly(args[0], args[1:])
	}
	return false
}

// goListWriteFlags make go list run other programs or write files: the
// debug flags write traces and action graphs to any path, -modfile names a
// go.mod it may rewrite, and the tool flags pass options such as
// -cpuprofile or -o to the compiler that -export runs.
var goListWriteFlags = []string{
	"toolexec", "exec", "debug-trace", "debug-runtime-trace", "debug-actiongraph",
	"modfile", "gcflags", "asmflags", "gccgoflags", "ldflags",
}

// goListReadOnly accepts go list unless a flag lets it write or execute.
// -mod=mod lets it rewrite go.mod and go.sum.
func goListReadOnly(args []string) bool {
	if hasGoFlag(args, goListWriteFlags...) {
		return false
	}
	for _, value := range goFlagValues(args, "mod") {
		if value != "readonly" && value != "vendor" {
			return false
		}
	}
	return true
}

// goFlagValues returns each value args give the named Go flag, spelled
// -name=v, --name=v, -name v or --name v. A flag with no value left yields
// an empty value, which callers treat like any unexpected one.
func goFlagValues(args []string, name string) []string {
	var values []string
	for i, arg := range args {
		flag, ok := strings.CutPrefix(arg, "-")
		if !ok {
			continue
		}
		flag, value, hasValue := strings.Cut(strings.TrimPrefix(flag, "-"), "=")
		if flag != name {
			continue
		}
		if !hasValue && i+1 < len(args) {
			value = args[i+1]
		}
		values = append(values, value)
	}
	return values
}

func isNodeTool(name string) bool {
	return name == "node" || name == "npm" || name == "pnpm" || name == "yarn" || name == "bun" || name == "deno"
}

var branchListFlags = map[string]bool{
	"-a": true, "--all": true, "-r": true, "--remotes": true, "-v": true, "-vv": true,
	"--verbose": true, "--list": true, "-l": true, "--show-current": true, "--no-color": true,
}

func gitReadOnly(sub string, rest []string) bool {
	// These flags write files or run configured external programs.
	if hasPrefix(rest, "--output", "--ext-diff", "--textconv", "-O", "--open-files-in-pager", "--exec") {
		return false
	}
	switch sub {
	case "status", "diff", "log", "show", "rev-parse", "ls-files", "blame", "shortlog",
		"describe", "show-ref", "rev-list", "merge-base", "grep", "cat-file":
		return true
	case "branch":
		for _, arg := range rest {
			if !branchListFlags[arg] {
				return false
			}
		}
		return true
	case "remote":
		return len(rest) == 0 || len(rest) == 1 && (rest[0] == "-v" || rest[0] == "--verbose")
	case "tag":
		return len(rest) == 0 || rest[0] == "-l" || rest[0] == "--list"
	case "stash":
		return len(rest) > 0 && (rest[0] == "list" || rest[0] == "show")
	case "config":
		return len(rest) > 0 && (rest[0] == "--get" || rest[0] == "--get-all" || rest[0] == "--get-regexp" || rest[0] == "--list" || rest[0] == "-l")
	}
	return false
}

// nodeScopeFlags point a package manager at another package or directory,
// so the package.json Likha fingerprinted would not be the one that runs.
var nodeScopeFlags = []string{"-w", "--workspace", "--workspaces", "--prefix", "-C", "--cwd", "--dir", "--filter", "-F", "-g", "--global", "-r", "--recursive"}

// verification recognizes the repository check commands that may run
// without a prompt once the user trusts the repository. The second result
// reports whether the command was recognized as a check at all.
func verification(words []string, sc scope) (Decision, bool) {
	name, args := words[0], words[1:]
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	root := sc.primary()
	switch name {
	case "npm":
		var script string
		var rest []string
		switch {
		case sub == "test" || sub == "t" || sub == "tst":
			script, rest = "test", args[1:]
		case (sub == "run" || sub == "run-script") && len(args) > 1:
			script, rest = args[1], args[2:]
		default:
			return Decision{}, false
		}
		// npm reads options before `--` itself; only test-runner arguments
		// after `--` are allowed.
		if len(rest) > 0 && rest[0] != "--" {
			return Decision{}, false
		}
		return scriptCheck(script, root)
	case "pnpm", "yarn":
		var script string
		var rest []string
		switch {
		case sub == "test" || sub == "t":
			script, rest = "test", args[1:]
		case sub == "run" && len(args) > 1:
			script, rest = args[1], args[2:]
		case verifyScripts[sub]:
			script, rest = sub, args[1:]
		default:
			return Decision{}, false
		}
		if hasAny(rest, nodeScopeFlags...) {
			return Decision{}, false
		}
		return scriptCheck(script, root)
	case "bun":
		if sub == "test" && !hasAny(args[1:], nodeScopeFlags...) {
			return check("bun", "", "bun test runner"), true
		}
		if sub == "run" && len(args) > 1 && !hasAny(args[2:], nodeScopeFlags...) {
			return scriptCheck(args[1], root)
		}
	case "go":
		// -vettool, like -toolexec, runs a program of the caller's choosing.
		if (sub == "test" || sub == "vet" || sub == "build") && !hasPrefix(args[1:], "-exec", "-toolexec", "--exec", "--toolexec", "-overlay", "-vettool", "--vettool") {
			return check("go", "", "go "+sub), true
		}
	case "cargo":
		switch sub {
		case "test", "check", "build", "clippy":
			if !hasPrefix(args[1:], "--fix", "--config", "-Z") {
				return check("cargo", "", "cargo "+sub), true
			}
		case "fmt":
			if hasAny(args[1:], "--check") {
				return check("cargo", "", "cargo fmt --check"), true
			}
		}
	case "pytest", "mypy":
		return check("python", "", name), true
	case "python", "python3":
		if sub == "-m" && len(args) > 1 && (args[1] == "pytest" || args[1] == "mypy") {
			return check("python", "", name+" -m "+args[1]), true
		}
	case "ruff":
		if sub == "check" && !hasPrefix(args[1:], "--fix", "--unsafe-fixes") {
			return check("python", "", "ruff check"), true
		}
	case "make":
		if len(args) == 1 && (sub == "test" || sub == "lint" || sub == "check" || sub == "build") {
			fingerprint, ok := makefileFingerprint(root)
			if !ok {
				return Decision{Tier: Ask, Reason: "no Makefile in the repository"}, true
			}
			return check("make", fingerprint, "make "+sub), true
		}
	}
	return Decision{}, false
}

func check(key, fingerprint, label string) Decision {
	return Decision{Tier: Verify, Reason: "verification check (" + label + ")", Check: key, Fingerprint: fingerprint}
}

// scriptCheck resolves a package.json script. The script text is scanned
// like a command: a test script that deletes files or pushes always asks.
func scriptCheck(name, root string) (Decision, bool) {
	if !verifyScripts[name] {
		return Decision{}, false
	}
	scripts, err := readScripts(root)
	if err != nil {
		return Decision{Tier: Ask, Reason: "package.json is missing or unreadable"}, true
	}
	main, ok := scripts[name]
	if !ok {
		return Decision{Tier: Ask, Reason: "package.json has no \"" + name + "\" script"}, true
	}
	sc := newScope(root)
	for _, hook := range []string{"pre" + name, name, "post" + name} {
		if text, ok := scripts[hook]; ok {
			tier, why := scanDanger(text, sc)
			if tier < Ask {
				return Decision{Tier: AlwaysAsk, Reason: "the \"" + hook + "\" script " + why, Script: main}, true
			}
			if why != "" {
				return Decision{Tier: Ask, Reason: "the \"" + hook + "\" script " + why, Script: main}, true
			}
		}
	}
	return Decision{
		Tier: Verify, Reason: "verification check (package script \"" + name + "\")",
		Check: "script:" + name, Fingerprint: scriptFingerprint(scripts, name), Script: main,
	}, true
}
