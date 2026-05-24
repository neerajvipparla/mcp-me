// MODULE: pkg/crawler/types/result.go
// PURPOSE: Defines the shared output types for all fetch strategies and the
//          pool. These are value types — no methods, no dependencies, no state.
//          All strategy and pool code depends on these; nothing depends on them
//          except consumers downstream (chunker, discovery).
//
// CORE DATA STRUCTURES:
//   - ContentFormat (int enum): FormatHTML / FormatMarkdown — drives ToMarkdown
//     dispatch in pkg/crawler/helper/convert.go.
//   - FetchResult: URL + content string + format + strategy name. Immutable after
//     creation; passed by pointer through the handler chain.
//   - PageResult: per-URL outcome from CrawlPool.FetchAll — wraps FetchResult
//     with its source URL and any error.
//
// TO MODIFY BEHAVIOR:
//   - Add a format: add a FormatXXX const and handle it in helper/convert.go.
//   - Add metadata to FetchResult: add fields here; update strategy constructors.
//
// DO NOT:
//   - Add methods to these types — they are plain data containers.
//   - Import any other package from here; this package is the leaf of the
//     crawler type dependency graph.
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
