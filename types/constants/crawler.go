package constants

const (
	// CRAWLER_DEFAULT_CONCURRENCY is the default number of worker goroutines
	// used by CrawlPool when the caller passes a non-positive concurrency value.
	// It also caps the number of in-flight strategy fetches per crawl job.
	CRAWLER_DEFAULT_CONCURRENCY = 5
)
