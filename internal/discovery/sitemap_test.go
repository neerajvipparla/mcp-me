package discovery_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neerajvipparla/mcp-me/internal/discovery"
)

func TestFetchSitemap_Standard(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sitemap.xml" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>%s/docs/a</loc></url><url><loc>%s/docs/b</loc></url></urlset>`, srvURL, srvURL)
	}))
	defer srv.Close()
	srvURL = srv.URL

	urls, err := discovery.FetchSitemap(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Errorf("expected 2 URLs, got %d: %v", len(urls), urls)
	}
}

func TestFetchSitemap_NotFound_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	urls, err := discovery.FetchSitemap(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if urls != nil {
		t.Errorf("expected nil when no sitemap, got %v", urls)
	}
}

func TestFetchSitemap_IndexWithChildren(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sitemap.xml":
			fmt.Fprintf(w, `<?xml version="1.0"?><sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>%s/sitemap-docs.xml</loc></sitemap></sitemapindex>`, srvURL)
		case "/sitemap-docs.xml":
			fmt.Fprintf(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>%s/docs/page1</loc></url></urlset>`, srvURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	urls, err := discovery.FetchSitemap(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 {
		t.Errorf("expected 1 URL from child sitemap, got %d", len(urls))
	}
}
