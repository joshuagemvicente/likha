package tooloutput

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	defaultOffset = 1
	defaultLimit  = 200
	// Bound JSON-escaped content too, leaving four KiB for page warnings and
	// caller-owned identity/envelope metadata within the shared 64 KiB cap.
	pageTextBytes = 60 << 10
)

// Read retrieves only a current-session artifact ID, never a path or URL.
// Zero offset/limit select defaults; negative values are rejected. Long lines
// are explicitly clipped when a whole line cannot fit in a single page, and
// NextOffset then advances to the following line (not a hidden byte offset).
// Callers should preserve their bounded preview if this returns an error.
func (s *Store) Read(id string, offset, limit int) (page Page, err error) {
	if !validID(id) {
		return page, errors.New("invalid retained output ID")
	}
	if offset < 0 || limit < 0 {
		return page, errors.New("output offset must be one-based and limit must be positive")
	}
	if offset == 0 {
		offset = defaultOffset
	}
	if limit == 0 {
		limit = defaultLimit
	}
	locked, err := s.lock(false)
	if err != nil {
		return page, err
	}
	defer func() { err = errors.Join(err, locked.close()) }()
	file, err := openPrivateFile(locked.dir, id+artifactSuffix, unix.O_RDONLY)
	if err != nil {
		return page, fmt.Errorf("retained output %s unavailable (not regenerated): %w", id, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close retained output: %w", closeErr))
		}
	}()
	meta, err := s.readHeader(file, id)
	if err != nil {
		return page, err
	}
	info, err := file.Stat()
	if err != nil {
		return page, fmt.Errorf("stat retained output: %w", err)
	}
	count := info.Size() - headerBytes
	if count < 0 || count > callBytes {
		return page, errors.New("retained output has an invalid size or exceeds the call cap")
	}
	data, err := io.ReadAll(io.NewSectionReader(file, headerBytes, count))
	if err != nil {
		return page, fmt.Errorf("read retained output: %w", err)
	}
	if int64(len(data)) != count {
		return page, errors.New("retained output changed or ended during retrieval")
	}
	page.Truncated = meta.Truncated
	if meta.Warning != "" {
		page.Warnings = append(page.Warnings, meta.Warning)
	}
	if !meta.Complete || meta.Bytes != count || meta.PlannedBytes != count {
		page.Truncated = true
		page.Warnings = append(page.Warnings, "capture did not finish or retained bytes changed; output may be incomplete")
	}
	text := string(data)
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "\uFFFD")
		page.Truncated = true
		page.Warnings = append(page.Warnings, "invalid or interrupted UTF-8 was replaced while reading")
	}
	return numberedPage(text, offset, limit, page), nil
}

func numberedPage(text string, offset, limit int, page Page) Page {
	position, lineNumber := 0, 1
	for lineNumber < offset && position < len(text) {
		next := strings.IndexByte(text[position:], '\n')
		if next < 0 {
			position = len(text)
			break
		}
		position += next + 1
		lineNumber++
	}
	if position == len(text) {
		return page
	}
	var out strings.Builder
	// Account for the actual escaped warning metadata, not just its raw
	// length. Reserve another 256 bytes for the long-line warning/next offset
	// and four KiB for the caller's tool-result envelope.
	envelope, _ := json.Marshal(page)
	budget := max(0, min(pageTextBytes, (64<<10)-(4<<10)-len(envelope)-256))
	emitted := 0
	for position < len(text) && emitted < limit {
		end := len(text)
		if newline := strings.IndexByte(text[position:], '\n'); newline >= 0 {
			end = position + newline
		}
		line := text[position:end]
		prefix := strconv.Itoa(lineNumber) + ": "
		available := budget - len(prefix) - 2 // JSON-escaped newline.
		take, cost := pagePrefix(line, max(0, available))
		if take < len(line) && emitted > 0 {
			break // Leave this entire line for the next page.
		}
		out.WriteString(prefix)
		out.WriteString(line[:take])
		out.WriteByte('\n')
		budget -= len(prefix) + cost + 2
		if take < len(line) {
			page.Truncated = true
			page.Warnings = append(page.Warnings, fmt.Sprintf("line %d exceeds the page byte cap; %d UTF-8 bytes omitted from this line", lineNumber, len(line)-take))
		}
		position = end
		if position < len(text) {
			position++ // Skip the source newline, not an extra trailing line.
		}
		lineNumber++
		emitted++
		if take < len(line) {
			break
		}
	}
	page.Content = out.String()
	if position < len(text) {
		page.NextOffset = lineNumber
		page.Truncated = true
	}
	return page
}

// Count encoding/json's escaped representation without allocating a second
// potentially huge line. The input has already been made valid UTF-8.
func pagePrefix(text string, budget int) (take, cost int) {
	for take < len(text) {
		r, size := utf8.DecodeRuneInString(text[take:])
		width := size
		switch r {
		case '\\', '"', '\b', '\f', '\n', '\r', '\t':
			width = 2
		case '<', '>', '&', '\u2028', '\u2029':
			width = 6
		default:
			if r < 0x20 {
				width = 6
			}
		}
		if cost+width > budget {
			break
		}
		take += size
		cost += width
	}
	return take, cost
}
