package discovery_test

import (
	"testing"

	"github.com/neerajvipparla/mcp-me/internal/discovery"
)

func TestFilter_AllowsSameDomain(t *testing.T) {
	f, err := discovery.NewFilter("https://docs.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{
		"https://docs.example.com/api/users",
		"https://docs.example.com/guide/start",
		"https://docs.example.com/reference/types",
	}
	for _, u := range allowed {
		if !f.Allow(u) {
			t.Errorf("expected Allow(%q) = true", u)
		}
	}
}

func TestFilter_BlocksExternalDomain(t *testing.T) {
	f, _ := discovery.NewFilter("https://docs.example.com/")
	if f.Allow("https://github.com/example/repo") {
		t.Error("should not allow external domain")
	}
}

func TestFilter_BlocksImages(t *testing.T) {
	f, _ := discovery.NewFilter("https://docs.example.com/")
	if f.Allow("https://docs.example.com/logo.png") {
		t.Error("should not allow .png")
	}
}

func TestFilter_BlocksBlogAndChangelog(t *testing.T) {
	f, _ := discovery.NewFilter("https://docs.example.com/")
	for _, u := range []string{
		"https://docs.example.com/blog/post",
		"https://docs.example.com/changelog/v2",
	} {
		if f.Allow(u) {
			t.Errorf("should not allow %q", u)
		}
	}
}

func TestFilter_PathPrefix_StaysWithinSubpath(t *testing.T) {
	f, err := discovery.NewFilter("https://example.com/docs/")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Allow("https://example.com/docs/api") {
		t.Error("should allow /docs/api when root is /docs/")
	}
	if f.Allow("https://example.com/about") {
		t.Error("should not allow /about when root is /docs/")
	}
}
