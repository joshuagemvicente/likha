package tooloutput

import (
	"strings"
	"testing"
)

const testSessionID = "0123456789abcdef0123456789abcdef"

// A run of empty lines still costs each line's number and newline, so it must
// end the page at the byte cap and continue on the next page, not overflow.
func TestReadKeepsEmptyLinesWithinPageCap(t *testing.T) {
	store, err := Open(t.TempDir(), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	const lines = 20000
	ref, err := store.Retain("call-1", strings.Repeat("\n", lines))
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for offset := 1; offset != 0; {
		page, err := store.Read(ref.ID, offset, lines)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Content) > pageTextBytes {
			t.Fatalf("page at offset %d holds %d bytes, over the %d-byte page cap", offset, len(page.Content), pageTextBytes)
		}
		got := strings.Count(page.Content, "\n")
		if got == 0 {
			t.Fatalf("page at offset %d made no progress", offset)
		}
		if page.NextOffset != 0 && page.NextOffset != offset+got {
			t.Fatalf("page at offset %d returned %d lines but next offset %d", offset, got, page.NextOffset)
		}
		seen += got
		offset = page.NextOffset
	}
	if seen != lines {
		t.Fatalf("pages returned %d lines, want %d", seen, lines)
	}
}
