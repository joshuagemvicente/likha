package model

import "testing"

func TestProviderRowFor(t *testing.T) {
	rows := []Provider{
		{Name: "first", BaseURL: "https://shared.example/v1"},
		{Name: "second", BaseURL: "https://shared.example/v1"},
		{Name: "other", BaseURL: "https://other.example/v1"},
	}
	cases := []struct {
		name     string
		base     string
		rows     []Provider
		wantName string
		wantOK   bool
	}{
		{"exact match", "https://other.example/v1", rows, "other", true},
		{"shared base takes the first row", "https://shared.example/v1", rows, "first", true},
		{"custom endpoint", "https://custom.example/v1", rows, "", false},
		{"no prefix match on a longer path", "https://other.example/v1/extra", rows, "", false},
		{"no prefix match on a shorter path", "https://other.example", rows, "", false},
		{"trailing slash is not normalized", "https://other.example/v1/", rows, "", false},
		{"empty base", "", rows, "", false},
		{"no rows", "https://other.example/v1", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row, ok := providerRowFor(tc.base, tc.rows)
			if ok != tc.wantOK || row.Name != tc.wantName {
				t.Fatalf("providerRowFor(%q) = (%q, %v), want (%q, %v)", tc.base, row.Name, ok, tc.wantName, tc.wantOK)
			}
		})
	}
}
