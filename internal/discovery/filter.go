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

	// If root has a subpath (e.g., /docs), only follow URLs within it
	if f.rootPath != "" {
		return strings.HasPrefix(lower, strings.ToLower(f.rootPath))
	}

	return true
}
