package helper

import (
	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/neerajvipparla/mcp-me/internal/crawler/types"
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
