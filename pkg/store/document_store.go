// MODULE: pkg/store/document_store.go
// PURPOSE: Implements Store using Qdrant Cloud server-side FastEmbed (MiniLM).
//          Owns the upsert and search paths for sentence-transformers/all-minilm-l6-v2.
//          Qdrant embeds text on ingestion and at query time — no external HTTP call.
//
// CORE DATA STRUCTURES:
//   - *qdrant.Client: shared gRPC connection, stateless per-call. Owned by main.go.
//   - []*qdrant.PointStruct (slice, bounded by batch size): built per Upsert call, not retained.
//
// TO MODIFY BEHAVIOR:
//   - Change model: update minilmModel constant — collection dims must match model output.
//   - Change collection dims: update minilmDims — must match before any data is written.
//   - Change distance metric: edit EnsureCollection — existing collections are unaffected.
//
// DO NOT:
//   - Import pkg/qdrantcfg here — client is injected; this file must not own connection logic.
//   - Change minilmDims after a collection exists — Qdrant will reject incompatible inserts.
//
// EXTENSION POINT: implement Store interface in a new file (e.g. vector_store.go for OpenAI).
//   This file remains unchanged.
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	qdrantgo "github.com/qdrant/go-client/qdrant"
)

const (
	EmbedderID  = "minilm"
	minilmModel = "sentence-transformers/all-minilm-l6-v2"
	minilmDims  = uint64(384)
)

// DocumentStore uses Qdrant Cloud's server-side FastEmbed inference.
// Upsert sends text + model name — Qdrant embeds on ingestion.
// Search wraps the query text in NewVectorInputDocument — Qdrant embeds at query time.
// No external embedding HTTP calls required.
type DocumentStore struct {
	client *qdrantgo.Client
}

// Compile-time proof that DocumentStore satisfies the Store interface.
var _ Store = (*DocumentStore)(nil)

func NewDocumentStore(client *qdrantgo.Client) *DocumentStore {
	return &DocumentStore{client: client}
}

func (s *DocumentStore) EmbedderID() string { return EmbedderID }

func (s *DocumentStore) EnsureCollection(ctx context.Context, name string) error {
	exists, err := s.client.CollectionExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return s.client.CreateCollection(ctx, &qdrantgo.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrantgo.NewVectorsConfig(&qdrantgo.VectorParams{
			Size:     minilmDims,
			Distance: qdrantgo.Distance_Cosine,
		}),
	})
}

func (s *DocumentStore) Upsert(ctx context.Context, collection string, texts []string, points []Point) error {
	if len(texts) != len(points) {
		return fmt.Errorf("store: texts/points mismatch %d vs %d", len(texts), len(points))
	}
	qpoints := make([]*qdrantgo.PointStruct, len(points))
	for i, p := range points {
		qpoints[i] = &qdrantgo.PointStruct{
			Id: qdrantgo.NewIDNum(uint64(uuid.New().ID())),
			Vectors: qdrantgo.NewVectorsDocument(&qdrantgo.Document{
				Text:  texts[i],
				Model: minilmModel,
			}),
			Payload: buildPayload(p),
		}
	}
	_, err := s.client.Upsert(ctx, &qdrantgo.UpsertPoints{
		CollectionName: collection,
		Points:         qpoints,
	})
	return err
}

func (s *DocumentStore) Search(ctx context.Context, collection, query string, topK uint64) ([]SearchResult, error) {
	resp, err := s.client.Query(ctx, &qdrantgo.QueryPoints{
		CollectionName: collection,
		Query: qdrantgo.NewQueryNearest(
			qdrantgo.NewVectorInputDocument(&qdrantgo.Document{
				Text:  query,
				Model: minilmModel,
			}),
		),
		Limit:       qdrantgo.PtrOf(topK),
		WithPayload: qdrantgo.NewWithPayload(true),
	})
	if err != nil {
		return nil, err
	}
	return scoredToResults(resp), nil
}

func (s *DocumentStore) GetByURL(ctx context.Context, collection, pageURL string) ([]SearchResult, error) {
	resp, err := s.client.Scroll(ctx, &qdrantgo.ScrollPoints{
		CollectionName: collection,
		Filter: &qdrantgo.Filter{
			Must: []*qdrantgo.Condition{
				qdrantgo.NewMatch("page_url", pageURL),
			},
		},
		WithPayload: qdrantgo.NewWithPayload(true),
		Limit:       qdrantgo.PtrOf(uint32(200)),
	})
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, len(resp))
	for i, p := range resp {
		out[i] = payloadToResult(p.Payload, 0)
	}
	return out, nil
}

func buildPayload(p Point) map[string]*qdrantgo.Value {
	return qdrantgo.NewValueMap(map[string]any{
		"text":         p.Text,
		"heading_path": p.HeadingPath,
		"page_url":     p.PageURL,
		"page_title":   p.PageTitle,
		"crawl_id":     p.CrawlID,
		"chunk_index":  float64(p.ChunkIndex),
	})
}

func scoredToResults(points []*qdrantgo.ScoredPoint) []SearchResult {
	out := make([]SearchResult, len(points))
	for i, p := range points {
		out[i] = payloadToResult(p.Payload, float32(p.Score))
	}
	return out
}

func payloadToResult(payload map[string]*qdrantgo.Value, score float32) SearchResult {
	return SearchResult{
		Text:        payload["text"].GetStringValue(),
		HeadingPath: payload["heading_path"].GetStringValue(),
		PageURL:     payload["page_url"].GetStringValue(),
		PageTitle:   payload["page_title"].GetStringValue(),
		Score:       score,
	}
}
