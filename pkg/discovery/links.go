package discovery

import (
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// ExtractLinks returns all unique absolute URLs found in <a href> elements of html.
// Relative URLs are resolved against baseURL. Fragments and query params are stripped.
func ExtractLinks(html string, baseURL string) ([]string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var links []string

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists || href == "" || strings.HasPrefix(href, "#") {
			return
		}

		ref, err := url.Parse(href)
		if err != nil {
			return
		}

		abs := base.ResolveReference(ref)
		abs.Fragment = ""
		abs.RawQuery = ""

		normalized := strings.TrimRight(abs.String(), "/")
		if normalized == "" {
			return
		}

		if !seen[normalized] {
			seen[normalized] = true
			links = append(links, normalized)
		}
	})

	return links, nil
}
