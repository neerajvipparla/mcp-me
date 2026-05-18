package discovery

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	crawlertypes "github.com/neerajvipparla/mcp-me/pkg/crawler/types"
)

// Discoverer finds all crawlable URLs for a documentation site.
// It tries sitemap.xml first; falls back to BFS link extraction.
type Discoverer struct {
	maxPages int
	filter   *Filter
	client   *http.Client
	handler  crawlertypes.Handler
}

// Option configures a Discoverer.
type Option func(*Discoverer)

// WithMaxPages caps the number of URLs returned. Default is 500.
func WithMaxPages(n int) Option {
	return func(d *Discoverer) { d.maxPages = n }
}

// WithHandler sets a fetch strategy chain used during BFS link extraction.
// When set, BFS fetches each page through the chain (enabling chromedp for CSR
// sites) instead of a plain HTTP GET. Only HTML results are used for link
// extraction; Markdown results (e.g. Firecrawl) are skipped.
func WithHandler(h crawlertypes.Handler) Option {
	return func(d *Discoverer) { d.handler = h }
}

// NewDiscoverer creates a Discoverer anchored to rootURL's domain.
func NewDiscoverer(rootURL string, opts ...Option) (*Discoverer, error) {
	filter, err := NewFilter(rootURL)
	if err != nil {
		return nil, err
	}
	d := &Discoverer{
		maxPages: 500,
		filter:   filter,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(d)
	}
	return d, nil
}

// Discover returns all crawlable URLs rooted at rootURL.
// Tries sitemap.xml first; uses BFS link extraction as fallback.
func (d *Discoverer) Discover(ctx context.Context, rootURL string) ([]string, error) {
	urls, err := FetchSitemap(ctx, rootURL)
	if err != nil {
		return nil, err
	}
	if len(urls) > 0 {
		return d.applyFilter(urls), nil
	}
	return d.bfs(ctx, rootURL)
}

func (d *Discoverer) applyFilter(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if d.filter.Allow(u) {
			out = append(out, u)
		}
	}
	return out
}

func (d *Discoverer) bfs(ctx context.Context, rootURL string) ([]string, error) {
	visited := make(map[string]bool)
	queue := []string{rootURL}

	for len(queue) > 0 && len(visited) < d.maxPages {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		url := queue[0]
		queue = queue[1:]

		if visited[url] {
			continue
		}
		visited[url] = true

		html, err := d.fetchHTML(ctx, url)
		if err != nil {
			continue // skip unreachable pages
		}

		links, err := ExtractLinks(html, url)
		if err != nil {
			continue
		}

		for _, link := range links {
			if !visited[link] && d.filter.Allow(link) {
				queue = append(queue, link)
			}
		}
	}

	urls := make([]string, 0, len(visited))
	for u := range visited {
		urls = append(urls, u)
	}
	return urls, nil
}

// fetchHTML returns the HTML content of url for link extraction.
// Uses the strategy chain when set (enables chromedp for CSR sites);
// falls back to a plain HTTP GET otherwise.
// Returns an empty string (no error) for Markdown results — link
// extraction from Markdown is not supported.
func (d *Discoverer) fetchHTML(ctx context.Context, url string) (string, error) {
	if d.handler != nil {
		result, err := d.handler.Handle(ctx, url)
		if err != nil {
			return "", err
		}
		if result.Format == crawlertypes.FormatMarkdown {
			return "", nil
		}
		return result.Content, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	return string(body), err
}
