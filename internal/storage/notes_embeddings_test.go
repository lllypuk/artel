package storage

import (
	"context"
	"testing"
)

func TestSaveAndDeleteEmbedding(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()

	// Create a note first.
	noteID, err := db.CreateNote(ctx, 1, "test", "content", "tag")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	// Save embedding.
	emb := []float32{0.1, 0.2, 0.3, 0.4}
	if err := db.SaveEmbedding(ctx, noteID, emb); err != nil {
		t.Fatalf("save embedding: %v", err)
	}

	// Verify embedding exists (indirectly via SearchSimilar).
	ids, err := db.SearchSimilar(ctx, emb, 10)
	if err != nil {
		t.Fatalf("search similar: %v", err)
	}
	if len(ids) != 1 || ids[0] != noteID {
		t.Errorf("expected [%d], got %v", noteID, ids)
	}

	// Delete embedding.
	if err := db.DeleteEmbedding(ctx, noteID); err != nil {
		t.Fatalf("delete embedding: %v", err)
	}

	// Verify it's gone.
	ids, err = db.SearchSimilar(ctx, emb, 10)
	if err != nil {
		t.Fatalf("search similar after delete: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected no results after delete, got %v", ids)
	}
}

func TestSaveEmbeddingReplace(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()
	noteID, err := db.CreateNote(ctx, 1, "test", "content", "tag")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	// Save first embedding.
	emb1 := []float32{0.1, 0.2}
	if err := db.SaveEmbedding(ctx, noteID, emb1); err != nil {
		t.Fatalf("save embedding 1: %v", err)
	}

	// Replace with different embedding.
	emb2 := []float32{0.9, 0.8}
	if err := db.SaveEmbedding(ctx, noteID, emb2); err != nil {
		t.Fatalf("save embedding 2: %v", err)
	}

	// Search with emb2 should find it with high similarity.
	ids, err := db.SearchSimilar(ctx, emb2, 10)
	if err != nil {
		t.Fatalf("search similar: %v", err)
	}
	if len(ids) != 1 || ids[0] != noteID {
		t.Errorf("expected [%d], got %v", noteID, ids)
	}
}

func TestSearchSimilarMultiple(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()

	// Create 3 notes with different embeddings.
	n1, _ := db.CreateNote(ctx, 1, "go", "golang programming", "dev")
	n2, _ := db.CreateNote(ctx, 1, "py", "python scripts", "dev")
	n3, _ := db.CreateNote(ctx, 1, "food", "recipes and cooking", "life")

	embGo := []float32{1.0, 0.0, 0.0}   // "programming" direction
	embPy := []float32{0.8, 0.2, 0.0}   // close to Go
	embFood := []float32{0.0, 0.0, 1.0} // orthogonal

	db.SaveEmbedding(ctx, n1, embGo)   //nolint:errcheck
	db.SaveEmbedding(ctx, n2, embPy)   //nolint:errcheck
	db.SaveEmbedding(ctx, n3, embFood) //nolint:errcheck

	// Query with "programming-like" embedding.
	query := []float32{0.9, 0.1, 0.0}
	ids, err := db.SearchSimilar(ctx, query, 3)
	if err != nil {
		t.Fatalf("search similar: %v", err)
	}

	// n1 (Go) should be first, n2 (Python) second, n3 (food) last.
	if len(ids) != 3 {
		t.Fatalf("expected 3 results, got %d: %v", len(ids), ids)
	}
	if ids[0] != n1 {
		t.Errorf("expected n1 (%d) first, got %d", n1, ids[0])
	}
	if ids[1] != n2 {
		t.Errorf("expected n2 (%d) second, got %d", n2, ids[1])
	}
	if ids[2] != n3 {
		t.Errorf("expected n3 (%d) third, got %d", n3, ids[2])
	}
}

func TestSearchSimilarLimit(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()

	for i := range 5 {
		nid, _ := db.CreateNote(ctx, 1, "note", "content", "")       //nolint:errcheck
		db.SaveEmbedding(ctx, nid, []float32{float32(i) * 0.1, 0.5}) //nolint:errcheck
	}

	query := []float32{0.5, 0.5}
	ids, err := db.SearchSimilar(ctx, query, 3)
	if err != nil {
		t.Fatalf("search similar: %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("expected 3 results (limit), got %d", len(ids))
	}
}

func TestSearchSimilarEmpty(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ids, err := db.SearchSimilar(context.Background(), []float32{0.1, 0.2}, 10)
	if err != nil {
		t.Fatalf("search similar: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty results, got %v", ids)
	}
}

func TestFloat32SerializeRoundtrip(t *testing.T) {
	original := []float32{0.0, -0.5, 1.0, 3.14159, 0.0001}
	blob := float32ToBytes(original)
	restored := bytesToFloat32(blob)

	if len(restored) != len(original) {
		t.Fatalf("length mismatch: %d vs %d", len(restored), len(original))
	}
	for i := range original {
		if original[i] != restored[i] {
			t.Errorf("index %d: expected %f, got %f", i, original[i], restored[i])
		}
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		a, b []float32
		want float32
	}{
		{[]float32{1, 0}, []float32{1, 0}, 1.0},
		{[]float32{1, 0}, []float32{0, 1}, 0.0},
		{[]float32{1, 0}, []float32{-1, 0}, -1.0},
		{[]float32{1, 1}, []float32{1, 1}, 1.0},
		{[]float32{}, []float32{1, 0}, 0.0},     // empty
		{[]float32{1, 0}, []float32{1}, 0.0},    // different lengths
		{[]float32{0, 0}, []float32{0, 0}, 0.0}, // zero vector
	}

	for _, tt := range tests {
		got := cosineSimilarity(tt.a, tt.b)
		if !floatEquals(got, tt.want) {
			t.Errorf("cosineSimilarity(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.want)
		}
	}
}

func floatEquals(a, b float32) bool {
	const eps = 0.0001
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < eps
}
