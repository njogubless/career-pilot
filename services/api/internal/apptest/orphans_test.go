package apptest

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/careerpilot/careerpilot/services/api/internal/documents"
)

func TestOrphanSweepRemovesOnlyOldUnreferencedObjects(t *testing.T) {
	app := New(t, Options{})
	c := app.Client(t)
	c.Register("orphans@example.com")
	var doc struct{ ID string }
	r := c.Upload("/api/v1/documents", "cv.pdf", pdfBytes, "CV")
	if r.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &doc)
	var key string
	if err := app.Pool.QueryRow(context.Background(), `SELECT storage_key FROM profile_documents WHERE id = $1`, doc.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = app.Store.PutAt(key, pdfBytes, old) // referenced, old: kept
	_ = app.Store.PutAt("documents/orphan-old", []byte("x"), old)
	_ = app.Store.PutAt("documents/orphan-new", []byte("x"), time.Now()) // possibly an upload in flight: kept
	_ = app.Store.PutAt("other/prefix", []byte("x"), old)                // outside the document prefix: ignored

	seen, removed, err := documents.SweepOrphans(context.Background(), app.Pool, app.Store, time.Now(), app.Log)
	if err != nil {
		t.Fatal(err)
	}
	if seen != 3 || removed != 1 {
		t.Fatalf("seen=%d removed=%d", seen, removed)
	}
	if !app.Store.Has(key) || app.Store.Has("documents/orphan-old") || !app.Store.Has("documents/orphan-new") || !app.Store.Has("other/prefix") {
		t.Fatal("wrong objects removed")
	}
	// The referenced document still downloads.
	if r := c.Do(http.MethodGet, "/api/v1/documents/"+doc.ID+"/download", nil); r.Code != http.StatusOK {
		t.Fatalf("download after sweep: %d", r.Code)
	}
}
