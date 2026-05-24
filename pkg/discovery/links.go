// MODULE: pkg/discovery/links.go
// PURPOSE: Extracts all unique absolute URLs from an HTML document's <a href>
//          elements. Resolves relative URLs against baseURL, strips fragments and
//          query params, and deduplicates. Called by Discoverer.bfs() per page.
//
// CORE DATA STRUCTURES:
//   - seen (map[string]bool): per-call dedup set; allocated fresh per ExtractLinks
//     invocation. Size bounded by number of <a> elements on the page.
//   - links ([]string): result slice; order matches DOM traversal order.
//
// TO MODIFY BEHAVIOR:
//   - Extract URLs from other attributes (e.g. <link href>): add a doc.Find() call.
//   - Preserve query params for specific patterns: add an exception before the
//     abs.RawQuery = "" line.
//
// DO NOT:
//   - Import pkg/crawler (cyclic via goquery shared usage).
//   - Add caching here — dedup lives in Discoverer.bfs() visited map.
//
// EXTENSION POINT: additional HTML link sources can be added as separate
//                  doc.Find() blocks without changing the function signature.
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
