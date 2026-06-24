// MODULE: pkg/discovery/discovery.go
// PURPOSE: Owns URL discovery for a documentation site. Tries sitemap.xml first
//          (fast, exhaustive); falls back to BFS link extraction when no sitemap
//          exists. Returns a deduplicated, filtered URL list capped at maxPages.
//
// CORE DATA STRUCTURES:
//   - Discoverer: holds Filter + maxPages + optional Handler chain + http.Client.
//     Stateless per Discover call — safe for concurrent use across jobs.
//   - visited (map[string]bool, BFS only): unbounded up to maxPages; allocated
//     fresh per Discover call, not retained on the struct.
//   - queue ([]string, BFS only): grows up to maxPages; sequential, no sorting needed.
//
// TO MODIFY BEHAVIOR:
//   - Change max pages: pass WithMaxPages option or edit default (500).
//   - Enable JS-aware BFS: pass WithHandler(chain) — fetchHTML will use the
//     strategy chain instead of plain HTTP.
//   - Add new discovery source: implement as a method like bfs() and call it as
//     a new fallback in Discover().
//
// DO NOT:
//   - Store job-scoped state on Discoverer — it is reused across calls.
//   - Import pkg/store or pkg/worker here (creates cycle).
//
// EXTENSION POINT: add new discovery strategies as methods on Discoverer and
//                  call them in Discover() as additional fallbacks; Filter and
//                  maxPages apply uniformly to all of them.
package discovery

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/logging"
	crawlertypes "github.com/neerajvipparla/mcp-me/pkg/crawler/types"
	"go.opentelemetry.io/otel/attribute"
)

// Discoverer finds all crawlable URLs for a documentation site.
// It tries sitemap.xml first; falls back to BFS link extraction.
type Discoverer struct {
	maxPages int
	filter   *Filter
	client   *http.Client
	handler  crawlertypes.Handler
	logger   *ion.Ion
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
		logger:   logging.Get(logging.TopicDiscovery),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d, nil
}

// Discover returns all crawlable URLs rooted at rootURL.
// Tries sitemap.xml first; uses BFS link extraction as fallback.
func (d *Discoverer) Discover(ctx context.Context, rootURL string) ([]string, error) {
	tracer := d.logger.Tracer("discovery")
	ctx, span := tracer.Start(ctx, "discovery")
	defer span.End()
	span.SetAttributes(attribute.String("root_url", rootURL))

	sitemapCtx, sitemapSpan := tracer.Start(ctx, "discovery.sitemap")
	urls, err := FetchSitemap(sitemapCtx, rootURL)
	if err != nil {
		sitemapSpan.RecordError(err)
		sitemapSpan.SetStatus(ion.StatusError, err.Error())
		sitemapSpan.End()
		return nil, err
	}
	if len(urls) > 0 {
		filtered := d.applyFilter(urls)
		sitemapSpan.SetAttributes(
			attribute.String("url", rootURL),
			attribute.Int("urls_found", len(filtered)),
		)
		sitemapSpan.End()
		if len(filtered) > 0 {
			span.SetAttributes(attribute.String("strategy", "sitemap"))
			d.logger.Info(ctx, "sitemap found",
				ion.String("file", "discovery.go"),
				ion.String("func", "Discover"),
				ion.String("url", rootURL),
				ion.String("url_count", fmt.Sprintf("%d", len(filtered))),
			)
			return filtered, nil
		}
		// Sitemap existed but all URLs were filtered (e.g. domain mismatch after
		// www-stripping still left nothing). Fall through to BFS.
		d.logger.Info(ctx, "sitemap found but all urls filtered: falling back to bfs",
			ion.String("file", "discovery.go"),
			ion.String("func", "Discover"),
			ion.String("url", rootURL),
			ion.String("sitemap_count", fmt.Sprintf("%d", len(urls))),
		)
	}
	sitemapSpan.SetAttributes(attribute.Int("urls_found", 0))
	sitemapSpan.End()

	span.SetAttributes(attribute.String("strategy", "bfs"))
	d.logger.Info(ctx, "no sitemap: falling back to bfs",
		ion.String("file", "discovery.go"),
		ion.String("func", "Discover"),
		ion.String("url", rootURL),
	)
	return d.bfs(ctx, tracer, rootURL)
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

func (d *Discoverer) bfs(ctx context.Context, tracer ion.Tracer, rootURL string) ([]string, error) {
	ctx, span := tracer.Start(ctx, "discovery.bfs")
	defer span.End()
	span.SetAttributes(attribute.String("root_url", rootURL))

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
	span.SetAttributes(
		attribute.Int("pages_visited", len(visited)),
		attribute.Int("urls_found", len(urls)),
	)
	d.logger.Info(ctx, "bfs complete",
		ion.String("file", "discovery.go"),
		ion.String("func", "bfs"),
		ion.String("root_url", rootURL),
		ion.String("url_count", fmt.Sprintf("%d", len(urls))),
	)
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
