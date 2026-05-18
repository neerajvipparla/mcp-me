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
// Lookup walks up the URL path, returning the first sitemap that yields URLs:
//  1. <rootURL>/sitemap.xml
//  2. <scheme>://<host>/<each-parent-path>/sitemap.xml (narrowest first)
//  3. <scheme>://<host>/sitemap.xml
//
// This handles sites that section their sitemaps by app (e.g. ClickHouse where
// the marketing site's /sitemap.xml has no docs URLs but /docs/sitemap.xml has
// thousands). Narrower matches win because they're more topical to the rootURL.
//
// Returns nil, nil if no candidate yields URLs (caller falls back to BFS).
// Handles sitemap index files transparently.
func FetchSitemap(ctx context.Context, rootURL string) ([]string, error) {
	for _, target := range sitemapCandidates(rootURL) {
		urls, err := fetchSitemapAt(ctx, target)
		if err != nil {
			return nil, err
		}
		if len(urls) > 0 {
			return urls, nil
		}
	}
	return nil, nil
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

func fetchChildSitemap(ctx context.Context, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
