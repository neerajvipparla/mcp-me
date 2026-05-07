package discovery_test

import (
	"testing"

	"github.com/neerajvipparla/mcp-me/internal/discovery"
)

func TestExtractLinks_Deduplicates(t *testing.T) {
	html := `<html><body>
		<a href="/docs/api">API</a>
		<a href="/docs/guide">Guide</a>
		<a href="/docs/api">API again</a>
		<a href="#section">Fragment only</a>
		<a href="">Empty</a>
	</body></html>`

	links, err := discovery.ExtractLinks(html, "https://docs.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Errorf("expected 2 unique links, got %d: %v", len(links), links)
	}
}

func TestExtractLinks_ResolvesRelative(t *testing.T) {
	html := `<html><body><a href="../reference/types">Types</a></body></html>`
	links, err := discovery.ExtractLinks(html, "https://docs.example.com/guide/intro")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0] != "https://docs.example.com/reference/types" {
		t.Errorf("unexpected resolved URL: %s", links[0])
	}
}

func TestExtractLinks_StripsQueryAndFragment(t *testing.T) {
	html := `<html><body><a href="/docs/api?version=2#section">API</a></body></html>`
	links, err := discovery.ExtractLinks(html, "https://docs.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0] != "https://docs.example.com/docs/api" {
		t.Errorf("expected stripped URL, got %s", links[0])
	}
}
