// MODULE: pkg/crawler/helper/convert.go
// PURPOSE: Converts a FetchResult to canonical Markdown so all downstream
//          consumers (chunker, store) receive a single format regardless of
//          which crawler strategy produced the content.
//
// CORE DATA STRUCTURES:
//   - htmlConverter (package-level singleton): html-to-markdown converter;
//     stateless after init — safe for concurrent use.
//
// TO MODIFY BEHAVIOR:
//   - Change conversion options: replace the nil options arg in NewConverter.
//   - Add a new format: extend the switch in ToMarkdown; add a FormatXXX const
//     in pkg/crawler/types/result.go.
//
// DO NOT:
//   - Call ToMarkdown during the crawl phase — only at MCP response time.
//   - Import pkg/store or pkg/mcp from here (creates cycle).
//
// EXTENSION POINT: add new ContentFormat cases to ToMarkdown without touching
//                  any other file in this package.
package helper

import (
	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/neerajvipparla/mcp-me/pkg/crawler/types"
)

var htmlConverter = md.NewConverter("", true, nil)

// ToMarkdown converts a FetchResult to markdown.
// FormatMarkdown results are returned as-is. FormatHTML is converted.
// Call this at MCP response time — not during crawl storage.
func ToMarkdown(r *types.FetchResult) (string, error) {
	if r.Format == types.FormatMarkdown {
		return r.Content, nil
	}
	return htmlConverter.ConvertString(r.Content)
}
