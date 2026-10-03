// Package update implements the read-only release-check used by the TUI:
// fetch the newest published tag from GitHub, decide whether it is newer than
// the running version, and throttle the network check so it runs at most once
// per day per machine. Failures are silent by contract: callers must treat any
// error as "no update available".
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultEndpoint = "https://api.github.com/repos/gem/likha/releases/latest"
	// apiOverrideEnv lets users point the check at a different endpoint,
	// mirroring the override the installer supports.
	apiOverrideEnv = "LIKHA_UPDATE_API"
)

// endpointClient bounds every request independently of the caller's context so
// a hung connection still can't stall the startup path.
var endpointClient = &http.Client{Timeout: 5 * time.Second}

// Latest returns the newest published release tag (e.g. "v0.2.0"). It speaks
// the GitHub releases API, reads at most 64KiB of the response body, and
// rejects payloads that do not carry a release-scheme tag ("v0.1.0", optional
// "-prerelease").
func Latest(ctx context.Context) (string, error) {
	endpoint := defaultEndpoint
	if override := os.Getenv(apiOverrideEnv); override != "" {
		endpoint = override
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := endpointClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api: %s", res.Status)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&payload); err != nil {
		return "", err
	}
	if _, ok := parse(payload.TagName); !ok {
		return "", fmt.Errorf("unexpected tag %q", payload.TagName)
	}
	return payload.TagName, nil
}

// Newer reports whether latest is a higher version than current. "dev" builds
// and unparseable versions never notify.
func Newer(current, latest string) bool {
	c, ok := parse(current)
	if !ok {
		return false
	}
	l, ok := parse(latest)
	if !ok {
		return false
	}
	return compareVersion(l, c) > 0
}

// version is a release-scheme version: exactly three numbers plus an optional
// prerelease suffix, which sorts before the plain release per semver.
type version struct {
	major, minor, patch int
	prerelease          string // without the leading '-'; empty for releases
}

// parse accepts only the release scheme ("v0.1.0", optional
// "-prerelease"), mirroring the regex scripts/release.sh uses for tags.
func parse(v string) (version, bool) {
	if !strings.HasPrefix(v, "v") {
		return version{}, false
	}
	rest := v[1:]
	var prerelease string
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		prerelease = rest[i+1:]
		rest = rest[:i]
	}
	if prerelease != "" && !validPrerelease(prerelease) {
		return version{}, false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var out version
	for i, p := range parts {
		if p == "" || !isNumeric(p) {
			return version{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return version{}, false
		}
		switch i {
		case 0:
			out.major = n
		case 1:
			out.minor = n
		default:
			out.patch = n
		}
	}
	out.prerelease = prerelease
	return out, true
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validPrerelease(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

// compareVersion reports whether a sorts higher than b. Numbers compare
// numerically; a release outranks its own prereleases of the same triple; two
// prereleases compare per semver identifier rules.
func compareVersion(a, b version) int {
	switch {
	case a.major != b.major:
		return cmpInt(a.major, b.major)
	case a.minor != b.minor:
		return cmpInt(a.minor, b.minor)
	case a.patch != b.patch:
		return cmpInt(a.patch, b.patch)
	case a.prerelease == b.prerelease:
		return 0
	case a.prerelease == "":
		return 1 // plain release outranks prerelease
	case b.prerelease == "":
		return -1
	}
	return comparePrerelease(a.prerelease, b.prerelease)
}

func comparePrerelease(a, b string) int {
	ai, bi := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ai) && i < len(bi); i++ {
		x, y := ai[i], bi[i]
		xn, yn := isNumeric(x), isNumeric(y)
		switch {
		case xn && yn:
			xiv, _ := strconv.Atoi(x)
			yiv, _ := strconv.Atoi(y)
			if xiv != yiv {
				return cmpInt(xiv, yiv)
			}
		case xn != yn:
			if xn { // numeric identifiers rank below alphanumeric
				return -1
			}
			return 1
		default:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		}
	}
	return cmpInt(len(ai), len(bi))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
