// MODULE: pkg/discovery/filter.go
// PURPOSE: Owns URL admission for the crawler. Enforces same-domain constraint,
//          path-prefix scoping, extension blocklist, and pattern blocklist.
//          A URL that does not pass Allow() is never fetched or enqueued.
//
// CORE DATA STRUCTURES:
//   - Filter: holds domain string, rootPath string, skip slices.
//     Immutable after NewFilter — safe for concurrent use.
//   - skipExtensions / skipPatterns ([]string): small fixed sets (<20 items);
//     linear scan is acceptable — no map needed.
//
// TO MODIFY BEHAVIOR:
//   - Block additional file types: add to defaultSkipExtensions.
//   - Block additional path patterns (e.g. /tags/): add to defaultSkipPatterns.
//   - Allow cross-domain crawling: remove the hostname check in Allow() — not
//     recommended, scope explosion risk.
//
// DO NOT:
//   - Make Filter mutable after construction (concurrent readers have no lock).
//   - Widen the rootPath check — it is the primary scope-explosion guard.
//
// EXTENSION POINT: construct a custom Filter with different skip lists by
//                  instantiating the struct directly (fields are exported-equivalent
//                  via NewFilter options if needed in future).
package discovery

import (
	"net/url"
	"path/filepath"
	"strings"
)

var defaultSkipExtensions = []string{".pdf", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".zip", ".tar", ".gz"}
var defaultSkipPatterns = []string{"/changelog/", "/blog/", "/releases/", "/news/"}

// Filter decides whether a URL should be followed during crawling.
type Filter struct {
	domain         string
	rootPath       string
	skipExtensions []string
	skipPatterns   []string
}

// NewFilter builds a Filter anchored to rootURL's domain and path.
func NewFilter(rootURL string) (*Filter, error) {
	u, err := url.Parse(rootURL)
	if err != nil {
		return nil, err
	}
	return &Filter{
		domain:         u.Hostname(),
		rootPath:       strings.TrimRight(u.Path, "/"),
		skipExtensions: defaultSkipExtensions,
		skipPatterns:   defaultSkipPatterns,
	}, nil
}

// Allow returns true if rawURL should be fetched and followed.
func (f *Filter) Allow(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}

	if u.Hostname() != f.domain {
		return false
	}

	path := u.Path
	lower := strings.ToLower(path)

	ext := strings.ToLower(filepath.Ext(path))
	for _, skip := range f.skipExtensions {
		if ext == skip {
			return false
		}
	}

	for _, pattern := range f.skipPatterns {
		if strings.Contains(lower, pattern) {
			return false
		}
	}

	// If root has a subpath (e.g., /docs), only follow URLs within it.
	// Check for a path-segment boundary after the prefix to avoid
	// /docs/integrations/go matching /docs/integrations/google-dataflow.
	if f.rootPath != "" {
		prefix := strings.ToLower(f.rootPath)
		if !strings.HasPrefix(lower, prefix) {
			return false
		}
		rest := lower[len(prefix):]
		if rest != "" && rest[0] != '/' {
			return false
		}
	}

	return true
}
