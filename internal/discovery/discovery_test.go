package discovery_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neerajvipparla/mcp-me/internal/discovery"
)

func TestDiscoverer_UsesSitemap(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sitemap.xml":
			fmt.Fprintf(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>%s/docs/a</loc></url><url><loc>%s/docs/b</loc></url></urlset>`, srvURL, srvURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	d, err := discovery.NewDiscoverer(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	urls, err := d.Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Errorf("expected 2 URLs from sitemap, got %d: %v", len(urls), urls)
	}
}

func TestDiscoverer_BFSFallback(t *testing.T) {
	pages := map[string]string{}
	var srvURL string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if content, ok := pages[r.URL.Path]; ok {
			fmt.Fprint(w, content)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	pages["/"] = fmt.Sprintf(`<html><body><a href="%s/docs/intro">Intro</a><a href="%s/docs/api">API</a></body></html>`, srvURL, srvURL)
	pages["/docs/intro"] = `<html><body><p>Introduction</p></body></html>`
	pages["/docs/api"] = `<html><body><p>API Reference</p></body></html>`

	d, err := discovery.NewDiscoverer(srv.URL, discovery.WithMaxPages(10))
	if err != nil {
		t.Fatal(err)
	}

	urls, err := d.Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) < 2 {
		t.Errorf("BFS should discover at least 2 pages, got %d: %v", len(urls), urls)
	}
}

func TestDiscoverer_RespectsMaxPages(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sitemap.xml" {
			http.NotFound(w, r)
			return
		}
		var links strings.Builder
		for i := 0; i < 5; i++ {
			links.WriteString(fmt.Sprintf(`<a href="%s%s/%d">page</a>`, srvURL, r.URL.Path, i))
		}
		fmt.Fprintf(w, `<html><body>%s</body></html>`, links.String())
	}))
	defer srv.Close()
	srvURL = srv.URL

	d, err := discovery.NewDiscoverer(srv.URL, discovery.WithMaxPages(5))
	if err != nil {
		t.Fatal(err)
	}

	urls, err := d.Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) > 5 {
		t.Errorf("expected at most 5 URLs, got %d", len(urls))
	}
}
