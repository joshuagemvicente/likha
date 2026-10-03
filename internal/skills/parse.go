package skills

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxSkillNameBytes = 64
	maxDescripBytes   = 1024
	maxSkillBodyBytes = 32 << 10
	// maxSkillFileBytes is the largest SKILL.md that is ever read whole. A
	// valid file cannot exceed the 32 KiB body plus a 1 KiB description, a
	// name, and the delimiter lines, so anything larger is rejected without
	// being loaded. Oversized-but-plausible files still fail the exact body
	// check with their real byte count.
	maxSkillFileBytes = maxSkillBodyBytes + (4 << 10)

	// Max error entry length in the Catalog. Error text must stay bounded
	// even when the offending input is not.
	maxErrorBytes = 256
)

// validSkillName enforces the shared skill-name rule: lowercase a-z and
// digits with interior hyphens only (no leading, trailing, or doubled
// hyphen), at most maxSkillNameBytes, never empty.
func validSkillName(name string) bool {
	if name == "" || len(name) > maxSkillNameBytes {
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

// parseSkillFile validates a SKILL.md and returns its frontmatter name, its
// description, and the Markdown body.
//
// The accepted format is a minimal hand-rolled YAML subset so that loading
// never pulls in a parser or enables an escape hatch:
//
//   - the first line is exactly "---";
//   - the only permitted keys are "name" and "description", each a single
//     "key: value" line whose value is trimmed; there is no multi-line
//     folding, no anchors, no comments, and no quoting rules — every other
//     nonblank line before the delimiter is rejected with a named error;
//   - blank lines are tolerated as separators before the delimiter (the
//     blank separator line in the canonical form is optional);
//   - a "---" or "..." line closes the frontmatter and every byte after it
//     is the body.
//
// Rejections are named so they can be surfaced in the catalog verbatim;
// nothing is silently included.
func parseSkillFile(data []byte) (name, description, body string, err error) {
	if !utf8.Valid(data) {
		return "", "", "", errors.New("SKILL.md is not valid UTF-8")
	}
	line, next := nextLine(data, 0)
	if trimCR(line) != "---" {
		return "", "", "", errors.New("SKILL.md must start with a --- frontmatter delimiter")
	}
	seen := make(map[string]bool, 2)
	for {
		if next >= len(data) {
			return "", "", "", errors.New("frontmatter is unterminated; it must end with a --- or ... delimiter")
		}
		line, next = nextLine(data, next)
		text := trimCR(line)
		switch text {
		case "---", "...":
			return finishSkill(name, description, data[next:], seen)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		colon := strings.IndexByte(text, ':')
		if colon < 0 {
			return "", "", "", fmt.Errorf("frontmatter line %q is malformed; expected \"key: value\"", clamp(text))
		}
		key := strings.TrimSpace(text[:colon])
		value := strings.TrimSpace(text[colon+1:])
		switch key {
		case "name":
			if seen["name"] {
				return "", "", "", errors.New("frontmatter name is set more than once")
			}
			seen["name"] = true
			name = value
		case "description":
			if seen["description"] {
				return "", "", "", errors.New("frontmatter description is set more than once")
			}
			seen["description"] = true
			description = value
		default:
			return "", "", "", fmt.Errorf("unsupported frontmatter field %q; only name and description are allowed", clamp(key))
		}
	}
}

// finishSkill checks the parsed metadata, bounds the body, and returns the
// final values.
func finishSkill(name, description string, rest []byte, seen map[string]bool) (string, string, string, error) {
	if !seen["name"] {
		return "", "", "", errors.New("frontmatter name is missing")
	}
	if name == "" {
		return "", "", "", errors.New("frontmatter name is empty")
	}
	if !seen["description"] {
		return "", "", "", errors.New("frontmatter description is missing")
	}
	if description == "" {
		return "", "", "", errors.New("frontmatter description is empty")
	}
	if len(name) > maxSkillNameBytes {
		return "", "", "", fmt.Errorf("frontmatter name is %d bytes; skill names are at most %d bytes",
			len(name), maxSkillNameBytes)
	}
	if !validSkillName(name) {
		return "", "", "", fmt.Errorf("frontmatter name %q is not a valid skill name; use 1-%d lowercase a-z characters, digits, or interior hyphens",
			clamp(name), maxSkillNameBytes)
	}
	if len(description) > maxDescripBytes {
		return "", "", "", fmt.Errorf("frontmatter description is %d bytes; the limit is %d bytes",
			len(description), maxDescripBytes)
	}
	body := string(rest)
	if len(body) > maxSkillBodyBytes {
		return "", "", "", fmt.Errorf("body is %d bytes; the limit is %d bytes", len(body), maxSkillBodyBytes)
	}
	if strings.TrimSpace(body) == "" {
		return "", "", "", errors.New("body is empty; SKILL.md must contain Markdown instruction text")
	}
	return name, description, body, nil
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
