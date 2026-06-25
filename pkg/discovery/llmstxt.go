// MODULE: pkg/discovery/llmstxt.go
// PURPOSE: Discovers crawlable URLs from a site's /llms.txt file.
//          llms.txt is an emerging standard (llmstxt.org) for publishing a
//          machine-readable index of documentation pages intended for AI tools.
//          Sites using Mintlify, GitBook, and newer Docusaurus deployments often
//          publish this. It is checked before sitemap because it represents an
//          explicit, AI-optimised URL list maintained by the site owner.
//
// FORMAT: Markdown file at <scheme>://<host>/llms.txt. Each section contains
//         markdown links in the form [Page Title](https://...) — only absolute
//         URLs are extracted; relative links are ignored.
//
// TO MODIFY BEHAVIOR:
//   - Change timeout: edit llmsClient initialization.
//   - Support relative links: resolve against the host before returning.
//
// DO NOT:
//   - Return an error for a missing or malformed file — treat as "not found"
//     so the caller falls through to sitemap.
package discovery

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/neerajvipparla/ion"
)

var llmsClient = &http.Client{Timeout: 10 * time.Second}

// matches absolute markdown links: [any text](https://... or http://...)
var mdLinkRe = regexp.MustCompile(`\[[^\]]*\]\((https?://[^)\s]+)\)`)

// FetchLLMSTxt fetches /llms.txt from the root of rootURL's domain and returns
// all absolute URLs found in markdown links. Returns nil, nil if the file does
// not exist or contains no links.
func FetchLLMSTxt(ctx context.Context, rootURL string) ([]string, error) {
	u, err := url.Parse(rootURL)
	if err != nil || u.Host == "" {
		return nil, nil
	}

	target := u.Scheme + "://" + u.Host + "/llms.txt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, nil
	}

	resp, err := llmsClient.Do(req)
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil
	}

	matches := mdLinkRe.FindAllSubmatch(body, -1)
	seen := make(map[string]bool, len(matches))
	urls := make([]string, 0, len(matches))
	for _, m := range matches {
		link := strings.TrimRight(string(m[1]), "/")
		if !seen[link] {
			seen[link] = true
			urls = append(urls, link)
		}
	}

	discoveryLogger.Info(ctx, "llms.txt fetched",
		ion.String("file", "llmstxt.go"),
		ion.String("func", "FetchLLMSTxt"),
		ion.String("url", target),
		ion.String("links_found", fmt.Sprintf("%d", len(urls))),
	)

	return urls, nil
}
