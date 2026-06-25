// MODULE: pkg/discovery/sitemap.go
// PURPOSE: Fetches and parses sitemap.xml (regular or sitemap index) for a
//          documentation root URL. Tries multiple candidate paths narrowest-first
//          so section-scoped sitemaps (e.g. /docs/sitemap.xml) are preferred over
//          the root sitemap. Also checks robots.txt for declared Sitemap: URLs.
//          Returns nil,nil when no usable sitemap is found so Discoverer can fall
//          back to BFS.
//
// CORE DATA STRUCTURES:
//   - sitemapIndex / urlset: XML decode targets; allocated per fetch, not retained.
//   - sitemapCandidates ([]string): O(path-depth) length; built once per call.
//   - sitemapClient (package-level): shared *http.Client with 15s timeout.
//
// TO MODIFY BEHAVIOR:
//   - Change fetch timeout: edit sitemapClient initialization.
//   - Add gzip support: wrap resp.Body in gzip.NewReader before io.ReadAll.
//   - Change candidate ordering: edit sitemapCandidates — currently narrowest first.
//
// DO NOT:
//   - Store per-request state at package level (sitemapClient is stateless).
//   - Return an error for a 404 — treat it as "no sitemap" (already the case).
package discovery

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var sitemapClient = &http.Client{Timeout: 15 * time.Second}

type sitemapLoc struct {
	Loc string `xml:"loc"`
}

type sitemapIndex struct {
	XMLName  xml.Name     `xml:"sitemapindex"`
	Sitemaps []sitemapLoc `xml:"sitemap"`
}

type urlset struct {
	XMLName xml.Name     `xml:"urlset"`
	URLs    []sitemapLoc `xml:"url"`
}

// FetchSitemap discovers URLs from sitemap.xml.
//
// Lookup order:
//  1. Path-based candidates narrowest-first (e.g. /docs/sitemap.xml before /sitemap.xml)
//  2. Sitemap URLs declared in /robots.txt Sitemap: headers
//
// Returns nil, nil if no candidate yields URLs (caller falls back to BFS).
// Handles sitemap index files transparently, including recursively nested indexes.
func FetchSitemap(ctx context.Context, rootURL string) ([]string, error) {
	// Path-based candidates (narrowest first)
	for _, target := range sitemapCandidates(rootURL) {
		urls, err := fetchSitemapAt(ctx, target)
		if err != nil {
			return nil, err
		}
		if len(urls) > 0 {
			return urls, nil
		}
	}

	// robots.txt Sitemap: header fallback
	for _, target := range robotsSitemapURLs(ctx, rootURL) {
		urls, err := fetchSitemapAt(ctx, target)
		if err != nil {
			continue
		}
		if len(urls) > 0 {
			return urls, nil
		}
	}

	return nil, nil
}

// robotsSitemapURLs fetches /robots.txt and returns all URLs from Sitemap: lines.
// Returns nil if robots.txt is missing, unreachable, or has no Sitemap: lines.
func robotsSitemapURLs(ctx context.Context, rootURL string) []string {
	u, err := url.Parse(rootURL)
	if err != nil || u.Host == "" {
		return nil
	}

	robotsURL := u.Scheme + "://" + u.Host + "/robots.txt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return nil
	}

	resp, err := sitemapClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var sitemaps []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "sitemap:") {
			raw := strings.TrimSpace(line[len("sitemap:"):])
			if raw != "" {
				sitemaps = append(sitemaps, raw)
			}
		}
	}
	return sitemaps
}

// sitemapCandidates returns sitemap URLs to try, narrowest first, deduped.
func sitemapCandidates(rootURL string) []string {
	trimmed := strings.TrimRight(rootURL, "/")
	candidates := []string{trimmed + "/sitemap.xml"}

	u, err := url.Parse(rootURL)
	if err != nil || u.Host == "" {
		return candidates
	}

	base := u.Scheme + "://" + u.Host
	path := strings.TrimRight(u.Path, "/")

	for path != "" {
		idx := strings.LastIndex(path, "/")
		if idx < 0 {
			break
		}
		path = path[:idx]
		candidates = append(candidates, base+path+"/sitemap.xml")
	}

	seen := make(map[string]bool, len(candidates))
	out := candidates[:0]
	for _, c := range candidates {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func fetchSitemapAt(ctx context.Context, target string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}

	resp, err := sitemapClient.Do(req)
	if err != nil {
		return nil, nil // treat network error as "no sitemap"
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sitemap fetch returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseSitemap(ctx, body)
}

// parseSitemap handles both <sitemapindex> and <urlset> XML.
// Sitemapindex entries are fetched recursively — nested indexes work at any depth.
func parseSitemap(ctx context.Context, data []byte) ([]string, error) {
	var idx sitemapIndex
	if xml.Unmarshal(data, &idx) == nil && len(idx.Sitemaps) > 0 {
		var all []string
		for _, s := range idx.Sitemaps {
			urls, err := fetchChildSitemap(ctx, s.Loc)
			if err != nil {
				continue // skip broken child sitemaps
			}
			all = append(all, urls...)
		}
		return all, nil
	}

	var us urlset
	if err := xml.Unmarshal(data, &us); err != nil {
		return nil, nil // not valid XML sitemap; caller falls back to BFS
	}

	urls := make([]string, 0, len(us.URLs))
	for _, u := range us.URLs {
		if u.Loc != "" {
			urls = append(urls, strings.TrimRight(u.Loc, "/"))
		}
	}
	return urls, nil
}

func fetchChildSitemap(ctx context.Context, sitemapURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sitemapURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := sitemapClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseSitemap(ctx, body)
}
