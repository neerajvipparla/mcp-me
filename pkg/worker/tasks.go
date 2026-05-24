// MODULE: pkg/worker/tasks.go
// PURPOSE: Defines Asynq task type constants and payload structs.
//          Single source of truth for task names — prevents string drift between
//          the enqueuer (api/crawl.go) and the handler (pipeline.go).
package worker

const TaskCrawlPipeline = "crawl:pipeline"

// CrawlPayload is serialised into the Asynq task and deserialised by PipelineHandler.
type CrawlPayload struct {
	CrawlID    string `json:"crawl_id"`
	URL        string `json:"url"`
	EmbedderID string `json:"embedder_id"`
}
