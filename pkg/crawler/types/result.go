package types

// ContentFormat describes the format of FetchResult.Content.
type ContentFormat int

const (
	FormatHTML     ContentFormat = iota
	FormatMarkdown               // Firecrawl returns markdown natively
)

// FetchResult is the output of a successful strategy fetch.
type FetchResult struct {
	URL      string
	Content  string
	Format   ContentFormat
	Strategy string // name of the strategy that produced this result
}

// PageResult is the output of CrawlPool.FetchAll for a single URL.
type PageResult struct {
	URL    string
	Result *FetchResult
	Err    error // non-nil means all strategies exhausted for this URL
}
