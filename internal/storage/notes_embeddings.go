package storage

import (
	"context"
	"encoding/binary"
	"math"
	"sort"
)

// SaveEmbedding stores the embedding vector for a note.
// Uses INSERT OR REPLACE to handle re-creation when note is updated.
func (s *DB) SaveEmbedding(ctx context.Context, noteID int64, embedding []float32) error {
	blob := float32ToBytes(embedding)
	_, err := s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO note_embeddings (note_id, embedding) VALUES (?, ?)",
		noteID, blob,
	)
	return err
}

// DeleteEmbedding removes the embedding for a note.
func (s *DB) DeleteEmbedding(ctx context.Context, noteID int64) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM note_embeddings WHERE note_id = ?",
		noteID,
	)
	return err
}

// SearchSimilar finds notes with embeddings most similar to the query embedding.
// Uses O(n) scan with cosine similarity in Go — sufficient for ~1000 notes.
// Returns note IDs sorted by similarity (highest first).
func (s *DB) SearchSimilar(ctx context.Context, queryEmbedding []float32, limit int) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT note_id, embedding FROM note_embeddings",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	type scored struct {
		noteID int64
		score  float32
	}
	var results []scored

	for rows.Next() {
		var noteID int64
		var blob []byte
		if err := rows.Scan(&noteID, &blob); err != nil {
			return nil, err
		}
		emb := bytesToFloat32(blob)
		sim := cosineSimilarity(queryEmbedding, emb)
		results = append(results, scored{noteID: noteID, score: sim})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort by similarity descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if limit > 0 && limit < len(results) {
		results = results[:limit]
	}

	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.noteID
	}
	return ids, nil
}

// float32ToBytes serialises []float32 to []byte (little-endian).
func float32ToBytes(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// bytesToFloat32 deserialises []byte to []float32 (little-endian).
func bytesToFloat32(blob []byte) []float32 {
	vec := make([]float32, len(blob)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return vec
}

// cosineSimilarity computes the cosine similarity between two vectors.
// Returns 0 for zero-length vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}
