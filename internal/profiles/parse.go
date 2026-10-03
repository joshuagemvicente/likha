package profiles

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxProfileNameBytes = 64
	maxDescripBytes     = 1024
	maxProfileBodyBytes = 32 << 10
	// maxProfileFileBytes is the largest AGENT.md that is ever read whole.
	// A valid file cannot exceed the 32 KiB body plus a 1 KiB description,
	// a name, and the delimiter lines, so anything larger is rejected
	// without being loaded. Oversized-but-plausible files still fail the
	// exact body check with their real byte count.
	maxProfileFileBytes = maxProfileBodyBytes + (4 << 10)

	// maxModelBytes bounds the optional model id shape. Whether the
	// configured provider actually serves the id is a dispatch decision,
	// not a parse decision.
	maxModelBytes = 128

	// Max error entry length in the Catalog. Error text must stay bounded
	// even when the offending input is not.
	maxErrorBytes = 256
)

// childCapableTools is the fixed set of tool names a profile allowlist may
// contain. ask_user is deliberately absent: FR-30 makes it main-only and
// child contexts have no interaction channel, so naming it is a definition
// error rather than an accepted-but-never-granted entry. Widening the set
// requires a spec amendment, not an entry here.
var childCapableTools = map[string]bool{
	"glob": true,
	"read": true,
	"grep": true,
	"task": true,
}

// validProfileName enforces the shared name rule: lowercase a-z and digits
// with interior hyphens only (no leading, trailing, or doubled hyphen), at
// most maxProfileNameBytes, never empty.
func validProfileName(name string) bool {
	if name == "" || len(name) > maxProfileNameBytes {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || name[i-1] == '-' {
				return false
			}
		default:
			return false
		}
	}
	// The loop checks each hyphen against the byte before it, so a trailing
	// hyphen slips past it; reject that separately.
	return name[len(name)-1] != '-'
}

// parseProfileFile validates an AGENT.md and returns its frontmatter name,
// description, and model, its normalized tools allowlist, and the Markdown
// body holding the profile instructions.
//
// The accepted format is a minimal hand-rolled YAML subset so that loading
// never pulls in a parser or enables an escape hatch:
//
//   - the first line is exactly "---";
//   - the only permitted keys are "name", "description", "model", and
//     "tools", each a single "key: value" line whose value is trimmed;
//     there is no multi-line folding, no anchors, no comments, and no
//     quoting rules — every other nonblank line before the delimiter is
//     rejected with a named error;
//   - "tools" parses on one line as either an inline bracketed list
//     "[read, grep]" or a bare comma list "read, grep";
//   - blank lines are tolerated as separators before the delimiter (the
//     blank separator line in the canonical form is optional);
//   - a "---" or "..." line closes the frontmatter and every byte after it
//     is the body.
//
// Rejections are named so they can be surfaced in the catalog verbatim;
// nothing is silently included.
func parseProfileFile(data []byte) (name, description, model string, tools []string, body string, err error) {
	if !utf8.Valid(data) {
		return "", "", "", nil, "", errors.New("AGENT.md is not valid UTF-8")
	}
	line, next := nextLine(data, 0)
	if trimCR(line) != "---" {
		return "", "", "", nil, "", errors.New("AGENT.md must start with a --- frontmatter delimiter")
	}
	seen := make(map[string]bool, 4)
	for {
		if next >= len(data) {
			return "", "", "", nil, "", errors.New("frontmatter is unterminated; it must end with a --- or ... delimiter")
		}
		line, next = nextLine(data, next)
		text := trimCR(line)
		switch text {
		case "---", "...":
			return finishProfile(name, description, model, tools, data[next:], seen)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		colon := strings.IndexByte(text, ':')
		if colon < 0 {
			return "", "", "", nil, "", fmt.Errorf("frontmatter line %q is malformed; expected \"key: value\"", clamp(text))
		}
		key := strings.TrimSpace(text[:colon])
		value := strings.TrimSpace(text[colon+1:])
		switch key {
		case "name":
			if seen["name"] {
				return "", "", "", nil, "", errors.New("frontmatter name is set more than once")
			}
			seen["name"] = true
			name = value
		case "description":
			if seen["description"] {
				return "", "", "", nil, "", errors.New("frontmatter description is set more than once")
			}
			seen["description"] = true
			description = value
		case "model":
			if seen["model"] {
				return "", "", "", nil, "", errors.New("frontmatter model is set more than once")
			}
			seen["model"] = true
			model = value
		case "tools":
			if seen["tools"] {
				return "", "", "", nil, "", errors.New("frontmatter tools is set more than once")
			}
			seen["tools"] = true
			tools, err = parseToolsValue(value)
			if err != nil {
				return "", "", "", nil, "", err
			}
		default:
			return "", "", "", nil, "", fmt.Errorf("unsupported frontmatter field %q; only name, description, model, and tools are allowed", clamp(key))
		}
	}
}

// parseToolsValue parses the one-line tools allowlist. Entries are trimmed,
// lowercased, and deduplicated preserving file order, and each must name a
// child-capable tool. An allowlist that is present but empty is a
// definition error: a profile that can invoke no tool cannot return
// findings.
func parseToolsValue(value string) ([]string, error) {
	text := value
	if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
		text = text[1 : len(text)-1]
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("tools is empty; a profile that can invoke no tool cannot return findings")
	}
	var tools []string
	seen := make(map[string]bool, 4)
	for _, raw := range strings.Split(text, ",") {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			return nil, errors.New("tools contains an empty entry")
		}
		if !childCapableTools[entry] {
			return nil, fmt.Errorf("tools names %q which is not a child-capable tool; the set is glob, read, grep, and task", clamp(entry))
		}
		if !seen[entry] {
			seen[entry] = true
			tools = append(tools, entry)
		}
	}
	return tools, nil
}

// finishProfile checks the parsed metadata, bounds the body, and returns
// the final values.
func finishProfile(name, description, model string, tools []string, rest []byte, seen map[string]bool) (string, string, string, []string, string, error) {
	if !seen["name"] {
		return "", "", "", nil, "", errors.New("frontmatter name is missing")
	}
	if name == "" {
		return "", "", "", nil, "", errors.New("frontmatter name is empty")
	}
	if !seen["description"] {
		return "", "", "", nil, "", errors.New("frontmatter description is missing")
	}
	if description == "" {
		return "", "", "", nil, "", errors.New("frontmatter description is empty")
	}
	if len(name) > maxProfileNameBytes {
		return "", "", "", nil, "", fmt.Errorf("frontmatter name is %d bytes; profile names are at most %d bytes",
			len(name), maxProfileNameBytes)
	}
	if !validProfileName(name) {
		return "", "", "", nil, "", fmt.Errorf("frontmatter name %q is not a valid profile name; use 1-%d lowercase a-z characters, digits, or interior hyphens",
			clamp(name), maxProfileNameBytes)
	}
	if len(description) > maxDescripBytes {
		return "", "", "", nil, "", fmt.Errorf("frontmatter description is %d bytes; the limit is %d bytes",
			len(description), maxDescripBytes)
	}
	if seen["model"] {
		if err := checkModelID(model); err != nil {
			return "", "", "", nil, "", err
		}
	}
	body := string(rest)
	if len(body) > maxProfileBodyBytes {
		return "", "", "", nil, "", fmt.Errorf("body is %d bytes; the limit is %d bytes", len(body), maxProfileBodyBytes)
	}
	if strings.TrimSpace(body) == "" {
		return "", "", "", nil, "", errors.New("body is empty; AGENT.md must contain Markdown instruction text")
	}
	return name, description, model, tools, body, nil
}

// checkModelID validates the optional model frontmatter shape: a nonempty
// single-line UTF-8 string of at most 128 bytes with no control characters
// and no whitespace. Whether the configured provider serves the id is
// enforced by the caller at dispatch.
func checkModelID(model string) error {
	if model == "" {
		return errors.New("frontmatter model is empty")
	}
	if len(model) > maxModelBytes {
		return fmt.Errorf("frontmatter model is %d bytes; the limit is %d bytes", len(model), maxModelBytes)
	}
	for _, r := range model {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("frontmatter model %q is not a valid model id; use 1-%d printable characters with no whitespace or control characters",
				clamp(model), maxModelBytes)
		}
	}
	return nil
}

// nextLine returns the line starting at off, excluding its newline, and the
// offset just past that newline. A final line without a newline still
// counts, ending the file.
func nextLine(data []byte, off int) (line []byte, next int) {
	rest := data[off:]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		return rest[:i], off + i + 1
	}
	return rest, len(data)
}

// trimCR removes a single trailing carriage return so CRLF files parse the
// same as LF files.
func trimCR(line []byte) string {
	s := string(line)
	if strings.HasSuffix(s, "\r") {
		s = s[:len(s)-1]
	}
	return s
}

// clamp bounds an error payload so a hostile file or path cannot inflate a
// catalog entry or returned error. Truncation happens on a rune boundary.
func clamp(s string) string {
	if len(s) <= maxErrorBytes {
		return s
	}
	cut := maxErrorBytes - 3 // room for the ellipsis
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
