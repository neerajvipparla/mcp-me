package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/neerajvipparla/mcp-me/internal/crawler/types"
	"github.com/neerajvipparla/mcp-me/internal/crawler/helper"
	"github.com/neerajvipparla/mcp-me/internal/crawler/strategies"
	"github.com/neerajvipparla/mcp-me/internal/discovery"
)

func main() {
	rootURL := "https://gobyexample.com"
	if len(os.Args) > 1 {
		rootURL = os.Args[1]
	}

	ctx := context.Background()

	fmt.Printf("→ Discovering URLs from %s\n", rootURL)
	d, err := discovery.NewDiscoverer(rootURL, discovery.WithMaxPages(10))
	if err != nil {
		log.Fatal(err)
	}

	urls, err := d.Discover(ctx, rootURL)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("→ Found %d URLs\n\n", len(urls))
	for _, u := range urls {
		fmt.Printf("  %s\n", u)
	}

	fmt.Printf("\n→ Fetching %d pages (concurrency=3)...\n\n", len(urls))
	chain := types.Chain(strategies.NewPlainHTTPHandler())
	pool := types.NewCrawlPool(chain, 3)
	results := pool.FetchAll(ctx, urls)

	ok, fail := 0, 0
	for _, r := range results {
		if r.Err != nil {
			fmt.Printf("  FAIL  %s\n        %v\n", r.URL, r.Err)
			fail++
			continue
		}
		md, err := helper.ToMarkdown(r.Result)
		if err != nil {
			fmt.Printf("  CONV  %s\n        %v\n", r.URL, err)
			fail++
			continue
		}
		preview := md
		if len(preview) > 120 {
			preview = preview[:120] + "…"
		}
		fmt.Printf("  OK    %-50s [%s] %d chars\n        %q\n", r.URL, r.Result.Strategy, len(md), preview)
		ok++
	}

	fmt.Printf("\n→ Done: %d ok, %d failed\n", ok, fail)
}
