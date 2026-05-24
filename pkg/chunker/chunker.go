// MODULE: pkg/chunker/chunker.go
// PURPOSE: Splits markdown into token-aware, heading-scoped chunks for vector storage.
//          Invariant: no chunk exceeds maxTokens; code fences are never split mid-block.
//
// CORE DATA STRUCTURES:
//   - []section (slice, unbounded): intermediate heading-delimited segments.
//     Access: sequential pass only. Growth: proportional to heading count in the doc.
//   - []Chunk (slice, unbounded): output returned to the pipeline / MCP add_page.
//     Growth: proportional to total token count ÷ targetTokens.
//
// TO MODIFY BEHAVIOR:
//   - Change token limits: edit targetTokens / maxTokens / overlapTokens constants.
//   - Change heading detection: edit splitByHeadings — impacts heading path and code-fence guard.
//   - Change overlap strategy: edit overlapWords — impacts chunk coherence for retrieval.
//
// DO NOT:
//   - Split inside code fences — the inCode toggle in splitByHeadings guards this invariant.
//   - Cache the tiktoken encoder across calls — GetEncoding is fast; caching adds mutable state.
//
// EXTENSION POINT: Split() is a plain function. Replace it at the call site
//   (pkg/worker/pipeline.go, pkg/mcp/tools.go) to swap in a different strategy.
//
// CHANGE SCENARIOS:
//   Different encoding: change "cl100k_base" in Split() — token counts change, nothing else.
//   Sentence-boundary split: add a pass in splitBySize() between word iteration and flush.
//   Add chunk metadata field: extend the Chunk struct and populate in both split paths.
package chunker

import (
	"strings"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

const (
	targetTokens  = 500
	maxTokens     = 800
	overlapTokens = 50
)

type Chunk struct {
	Text        string
	HeadingPath string
	ChunkIndex  int
}

// Time: O(n) where n = total tokens in markdown; Space: O(n)
// DS: []section slice built in one pass, then []Chunk built in second pass.
func Split(markdown string) ([]Chunk, error) {
	enc, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		return nil, err
	}

	sections := splitByHeadings(markdown)
	var chunks []Chunk
	idx := 0

	for _, sec := range sections {
		if strings.TrimSpace(sec.body) == "" {
			continue
		}
		toks := len(enc.Encode(sec.body, nil, nil))
		if toks <= maxTokens {
			chunks = append(chunks, Chunk{
				Text:        strings.TrimSpace(sec.body),
				HeadingPath: sec.headingPath,
				ChunkIndex:  idx,
			})
			idx++
		} else {
			sub := splitBySize(sec.body, sec.headingPath, enc, &idx)
			chunks = append(chunks, sub...)
		}
	}
	return chunks, nil
}

type section struct {
	headingPath string
	body        string
}

func splitByHeadings(md string) []section {
	lines := strings.Split(md, "\n")
	var sections []section
	var cur section
	headings := make([]string, 7)
	inCode := false

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
		}
		if !inCode && strings.HasPrefix(line, "#") {
			if strings.TrimSpace(cur.body) != "" {
				sections = append(sections, cur)
			}
			level := 0
			for _, c := range line {
				if c == '#' {
					level++
				} else {
					break
				}
			}
			title := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if level <= 6 {
				headings[level] = title
				for i := level + 1; i <= 6; i++ {
					headings[i] = ""
				}
			}
			cur = section{headingPath: buildPath(headings), body: line + "\n"}
		} else {
			cur.body += line + "\n"
		}
	}
	if strings.TrimSpace(cur.body) != "" {
		sections = append(sections, cur)
	}
	return sections
}

func buildPath(headings []string) string {
	var parts []string
	for i := 1; i <= 6; i++ {
		if headings[i] != "" {
			parts = append(parts, headings[i])
		}
	}
	return strings.Join(parts, " > ")
}

func splitBySize(text, headingPath string, enc *tiktoken.Tiktoken, idx *int) []Chunk {
	words := strings.Fields(text)
	var chunks []Chunk
	var buf []string
	tokCount := 0

	flush := func() {
		if len(buf) == 0 {
			return
		}
		chunks = append(chunks, Chunk{
			Text:        strings.Join(buf, " "),
			HeadingPath: headingPath,
			ChunkIndex:  *idx,
		})
		*idx++
		keep := overlapWords(buf, enc, overlapTokens)
		buf = keep
		tokCount = len(enc.Encode(strings.Join(buf, " "), nil, nil))
	}

	for _, w := range words {
		wToks := len(enc.Encode(w, nil, nil))
		if tokCount+wToks > targetTokens {
			flush()
		}
		buf = append(buf, w)
		tokCount += wToks
	}
	flush()
	return chunks
}

func overlapWords(words []string, enc *tiktoken.Tiktoken, maxToks int) []string {
	for i := len(words) - 1; i >= 0; i-- {
		sub := words[i:]
		if len(enc.Encode(strings.Join(sub, " "), nil, nil)) <= maxToks {
			return sub
		}
	}
	return nil
}
