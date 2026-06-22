// MODULE: pkg/store/document_store.go
// PURPOSE: Implements Store using Qdrant Cloud server-side FastEmbed.
//          Dense vectors: sentence-transformers/all-minilm-l6-v2 (384d, cosine).
//          Sparse vectors: Qdrant/bm25 (IDF-weighted term frequency, server-side).
//          Search uses Prefetch(dense) + Prefetch(sparse) fused with RRF — no manual α tuning.
//
// CORE DATA STRUCTURES:
//   - *qdrant.Client: shared gRPC connection, stateless per-call. Owned by main.go.
//   - []*qdrant.PointStruct (slice, bounded by batch size): built per Upsert call, not retained.
//
// TO MODIFY BEHAVIOR:
//   - Change dense model: update minilmModel + minilmDims — collection must be rebuilt.
//   - Change sparse model: update bm25Model — collection must be rebuilt.
//   - Change candidate pool size: edit candidateMult — higher = better RRF recall, more latency.
//
// DO NOT:
//   - Import pkg/qdrantcfg here — client is injected; this file must not own connection logic.
//   - Change minilmDims after a collection exists — Qdrant will reject incompatible inserts.
//   - Use unnamed (default) VectorsConfig — all new collections use named dense+sparse vectors.
//
// EXTENSION POINT: implement Store interface in a new file (e.g. openai_store.go).
//   This file remains unchanged.
package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/logging"
	qdrantgo "github.com/qdrant/go-client/qdrant"
	"go.opentelemetry.io/otel/attribute"
)

const (
	EmbedderID    = "minilm"
	minilmModel   = "sentence-transformers/all-minilm-l6-v2"
	bm25Model     = "Qdrant/bm25"
	minilmDims    = uint64(384)
	denseVec      = "dense"
	sparseVec     = "sparse"
	candidateMult = uint64(3) // fetch candidateMult*topK per leg before RRF fusion
)

// DocumentStore uses Qdrant Cloud's server-side FastEmbed inference.
// Both dense (MiniLM) and sparse (BM25) vectors are computed by Qdrant — no local model.
// Search runs two prefetch legs (dense + sparse) fused via RRF.
type DocumentStore struct {
	client *qdrantgo.Client
	logger *ion.Ion
}

// Compile-time proof that DocumentStore satisfies the Store interface.
var _ Store = (*DocumentStore)(nil)

func NewDocumentStore(client *qdrantgo.Client) *DocumentStore {
	return &DocumentStore{client: client, logger: logging.Get(logging.TopicStore)}
}

func (s *DocumentStore) EmbedderID() string { return EmbedderID }

// EnsureCollection creates a hybrid (dense + sparse) collection if it does not exist.
// If an old single-vector collection is detected it is dropped and rebuilt — callers
// should treat this as a destructive migration and re-ingest their data.
func (s *DocumentStore) EnsureCollection(ctx context.Context, name string) error {
	tracer := s.logger.Tracer("store")
	ctx, span := tracer.Start(ctx, "store.ensure_collection")
	defer span.End()
	span.SetAttributes(attribute.String("collection", name))

	exists, err := s.client.CollectionExists(ctx, name)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		return err
	}
	if exists {
		info, err := s.client.GetCollectionInfo(ctx, name)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(ion.StatusError, err.Error())
			return err
		}
		pm := info.GetConfig().GetParams().GetVectorsConfig().GetParamsMap()
		if pm != nil {
			if _, hasDense := pm.GetMap()[denseVec]; hasDense {
				span.SetAttributes(attribute.String("action", "ready"))
				s.logger.Info(ctx, "collection ready",
					ion.String("file", "document_store.go"),
					ion.String("func", "EnsureCollection"),
					ion.String("collection", name),
				)
				// Collection exists with correct schema — ensure page_url index
				// exists even if collection pre-dates this requirement.
				return s.ensurePageURLIndex(ctx, name)
			}
		}
		// Old single-vector schema — drop and recreate.
		span.SetAttributes(attribute.String("action", "migrating"))
		s.logger.Warn(ctx, "legacy collection detected: migrating",
			ion.String("file", "document_store.go"),
			ion.String("func", "EnsureCollection"),
			ion.String("collection", name),
		)
		if err := s.client.DeleteCollection(ctx, name); err != nil {
			span.RecordError(err)
			span.SetStatus(ion.StatusError, err.Error())
			return fmt.Errorf("drop legacy collection %s: %w", name, err)
		}
	}
	span.SetAttributes(attribute.String("action", "created"))
	s.logger.Info(ctx, "creating collection",
		ion.String("file", "document_store.go"),
		ion.String("func", "EnsureCollection"),
		ion.String("collection", name),
	)
	if err := s.client.CreateCollection(ctx, &qdrantgo.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrantgo.NewVectorsConfigMap(map[string]*qdrantgo.VectorParams{
			denseVec: {Size: minilmDims, Distance: qdrantgo.Distance_Cosine},
		}),
		SparseVectorsConfig: qdrantgo.NewSparseVectorsConfig(map[string]*qdrantgo.SparseVectorParams{
			sparseVec: {Modifier: qdrantgo.Modifier_Idf.Enum()},
		}),
	}); err != nil {
		return err
	}
	return s.ensurePageURLIndex(ctx, name)
}

// ensurePageURLIndex creates a keyword payload index on page_url if it does not
// already exist. Qdrant is idempotent — calling this on a collection that already
// has the index is a no-op.
func (s *DocumentStore) ensurePageURLIndex(ctx context.Context, name string) error {
	wait := true
	_, err := s.client.CreateFieldIndex(ctx, &qdrantgo.CreateFieldIndexCollection{
		CollectionName: name,
		FieldName:      "page_url",
		FieldType:      qdrantgo.FieldType_FieldTypeKeyword.Enum(),
		Wait:           &wait,
	})
	return err
}

// Time: O(n) where n = len(texts); dominated by Qdrant gRPC round-trip + server-side embedding.
func (s *DocumentStore) Upsert(ctx context.Context, collection string, texts []string, points []Point) error {
	if len(texts) != len(points) {
		return fmt.Errorf("store: texts/points mismatch %d vs %d", len(texts), len(points))
	}

	tracer := s.logger.Tracer("store")
	ctx, span := tracer.Start(ctx, "store.upsert")
	defer span.End()
	span.SetAttributes(
		attribute.String("collection", collection),
		attribute.Int("count", len(points)),
	)

	qpoints := make([]*qdrantgo.PointStruct, len(points))
	for i, p := range points {
		qpoints[i] = &qdrantgo.PointStruct{
			Id: qdrantgo.NewIDNum(uint64(uuid.New().ID())),
			Vectors: qdrantgo.NewVectorsMap(map[string]*qdrantgo.Vector{
				denseVec:  qdrantgo.NewVectorDocument(&qdrantgo.Document{Text: texts[i], Model: minilmModel}),
				sparseVec: qdrantgo.NewVectorDocument(&qdrantgo.Document{Text: texts[i], Model: bm25Model}),
			}),
			Payload: buildPayload(p),
		}
	}
	_, err := s.client.Upsert(ctx, &qdrantgo.UpsertPoints{
		CollectionName: collection,
		Points:         qpoints,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		s.logger.Error(ctx, "upsert failed", err,
			ion.String("file", "document_store.go"),
			ion.String("func", "Upsert"),
			ion.String("collection", collection),
			ion.String("count", fmt.Sprintf("%d", len(points))),
		)
		return err
	}
	s.logger.Info(ctx, "upsert complete",
		ion.String("file", "document_store.go"),
		ion.String("func", "Upsert"),
		ion.String("collection", collection),
		ion.String("count", fmt.Sprintf("%d", len(points))),
	)
	return nil
}

// Search runs dense and sparse prefetch legs in parallel inside Qdrant, fuses with RRF.
// Each leg fetches candidateMult*topK candidates; RRF re-ranks and returns topK.
// Time: O(k) where k = topK; dominated by Qdrant network round-trip + two-leg server search.
func (s *DocumentStore) Search(ctx context.Context, collection, query string, topK uint64) ([]SearchResult, error) {
	tracer := s.logger.Tracer("store")
	ctx, span := tracer.Start(ctx, "store.search")
	defer span.End()
	span.SetAttributes(
		attribute.String("collection", collection),
		attribute.String("query", query),
		attribute.Int64("top_k", int64(topK)),
	)

	candidateK := topK * candidateMult

	resp, err := s.client.Query(ctx, &qdrantgo.QueryPoints{
		CollectionName: collection,
		Prefetch: []*qdrantgo.PrefetchQuery{
			{
				Query: qdrantgo.NewQueryDocument(&qdrantgo.Document{Text: query, Model: minilmModel}),
				Using: qdrantgo.PtrOf(denseVec),
				Limit: qdrantgo.PtrOf(candidateK),
			},
			{
				Query: qdrantgo.NewQueryDocument(&qdrantgo.Document{Text: query, Model: bm25Model}),
				Using: qdrantgo.PtrOf(sparseVec),
				Limit: qdrantgo.PtrOf(candidateK),
			},
		},
		Query:       qdrantgo.NewQueryFusion(qdrantgo.Fusion_RRF),
		Limit:       qdrantgo.PtrOf(topK),
		WithPayload: qdrantgo.NewWithPayload(true),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		return nil, err
	}
	results := scoredToResults(resp)
	span.SetAttributes(attribute.Int("results", len(results)))
	return results, nil
}

// GetByURL returns all stored chunks for a specific page URL via payload filter scroll.
// Time: O(n) where n = chunks stored for pageURL; dominated by Qdrant scroll.
func (s *DocumentStore) GetByURL(ctx context.Context, collection, pageURL string) ([]SearchResult, error) {
	pageURL = strings.TrimRight(pageURL, "/")

	tracer := s.logger.Tracer("store")
	ctx, span := tracer.Start(ctx, "store.get_by_url")
	defer span.End()
	span.SetAttributes(
		attribute.String("collection", collection),
		attribute.String("page_url", pageURL),
	)

	// Ensure the keyword index exists before filtering. Collections created
	// before this requirement was added won't have it; CreateFieldIndex is
	// idempotent so this is a no-op for collections that already have it.
	if err := s.ensurePageURLIndex(ctx, collection); err != nil {
		s.logger.Warn(ctx, "failed to ensure page_url index",
			ion.String("file", "document_store.go"),
			ion.String("func", "GetByURL"),
			ion.String("collection", collection),
			ion.String("error", err.Error()),
		)
	}

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
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		return nil, err
	}
	out := make([]SearchResult, len(resp))
	for i, p := range resp {
		out[i] = payloadToResult(p.Payload, 0)
	}
	span.SetAttributes(attribute.Int("chunks_returned", len(out)))
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
