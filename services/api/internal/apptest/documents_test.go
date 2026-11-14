package apptest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/documents"
	"github.com/careerpilot/careerpilot/services/api/internal/jobs"
)

var pdfBytes = []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF")

func docxBytes(t *testing.T) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte("<w:document/>"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocumentUploadValidationAndIsolation(t *testing.T) {
	app := New(t, Options{})
	alice := app.Client(t)
	alice.Register("docs-a@example.com")
	bob := app.Client(t)
	bob.Register("docs-b@example.com")

	cases := []struct {
		name, file string
		data       []byte
		want       int
	}{
		{"pdf", "cv.pdf", pdfBytes, http.StatusCreated},
		{"docx", "cv.docx", docxBytes(t), http.StatusCreated},
		{"exe disguised", "cv.pdf", []byte("MZ\x90\x00binary"), http.StatusUnprocessableEntity},
		{"pdf with wrong ext", "cv.html", pdfBytes, http.StatusUnprocessableEntity},
		{"zip not docx", "cv.docx", func() []byte {
			var b bytes.Buffer
			zw := zip.NewWriter(&b)
			w, _ := zw.Create("evil.txt")
			_, _ = w.Write([]byte("x"))
			_ = zw.Close()
			return b.Bytes()
		}(), http.StatusUnprocessableEntity},
		{"too large", "big.pdf", append([]byte("%PDF-"), make([]byte, 3<<20)...), http.StatusRequestEntityTooLarge},
	}
	var docID string
	for _, tc := range cases {
		r := alice.Upload("/api/v1/documents", tc.file, tc.data, "CV")
		if r.Code != tc.want {
			t.Errorf("%s: got %d want %d (%s)", tc.name, r.Code, tc.want, r.Body)
		}
		if tc.name == "pdf" {
			var d struct{ ID string }
			r.JSON(t, &d)
			docID = d.ID
		}
	}

	r := alice.Do(http.MethodGet, "/api/v1/documents/"+docID+"/download", nil)
	if r.Code != http.StatusOK || !bytes.Equal(r.Body, pdfBytes) {
		t.Fatalf("download: %d", r.Code)
	}
	if !strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment") || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe download headers: %v", r.Header)
	}

	// Bob cannot download, delete or extract Alice's document.
	if r := bob.Do(http.MethodGet, "/api/v1/documents/"+docID+"/download", nil); r.Code != http.StatusNotFound {
		t.Fatalf("cross-user download: %d", r.Code)
	}
	if r := bob.Do(http.MethodDelete, "/api/v1/documents/"+docID, nil); r.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete: %d", r.Code)
	}
	var list struct{ Data []any }
	bob.Do(http.MethodGet, "/api/v1/documents", nil).JSON(t, &list)
	if len(list.Data) != 0 {
		t.Fatal("bob lists alice's documents")
	}

	before := app.Store.Len()
	if r := alice.Do(http.MethodDelete, "/api/v1/documents/"+docID, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", r.Code)
	}
	if app.Store.Len() != before-1 {
		t.Fatal("stored object not removed on delete")
	}
}

func TestUploadWithoutStorageIsHonest(t *testing.T) {
	app := New(t, Options{NoStorage: true})
	c := app.Client(t)
	c.Register("nostore@example.com")
	r := c.Upload("/api/v1/documents", "cv.pdf", pdfBytes, "CV")
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", r.Code)
	}
}

func TestExtractionRequiresConfiguredProvider(t *testing.T) {
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"llm_configured": false, "llm_provider": "none"})
	}))
	defer ai.Close()
	app := New(t, Options{AIURL: ai.URL})
	c := app.Client(t)
	c.Register("noai@example.com")
	var d struct{ ID string }
	c.Upload("/api/v1/documents", "cv.pdf", pdfBytes, "CV").JSON(t, &d)
	r := c.Do(http.MethodPost, "/api/v1/documents/"+d.ID+"/extractions", nil)
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no provider, got %d %s", r.Code, r.Body)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM profile_extractions`); n != 0 {
		t.Fatal("extraction must not be queued without a provider")
	}
}

const fakeProposal = `{"personal":{"headline":"Backend Engineer","summary":"Builds APIs.","location_country":"KE","links":[]},
"experiences":[{"employer":"Acme","title":"Engineer","start_date":"2021-01","end_date":null,"is_current":true,"evidence":"Engineer, Acme","evidence_found":true}],
"education":[],"skills":[{"name":"Go","category":"Language","evidence":"Go","evidence_found":true}],"certifications":[],"projects":[],"warnings":[]}`

func TestExtractionEndToEndThroughWorker(t *testing.T) {
	var gotToken string
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v1/info":
			_ = json.NewEncoder(w).Encode(map[string]any{"llm_configured": true, "llm_provider": "fake"})
		case "/internal/v1/cv/extract":
			gotToken = r.Header.Get("Authorization")
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["mime_type"] != documents.MimePDF || req["content_base64"] == "" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			_, _ = w.Write([]byte(`{"proposal":` + fakeProposal + `,"meta":{"provider":"fake","model":"fake-1","prompt_version":"cv_extract@1","usage":{"input_tokens":10,"output_tokens":20}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ai.Close()

	app := New(t, Options{AIURL: ai.URL})
	c := app.Client(t)
	c.Register("extract@example.com")
	var d struct{ ID string }
	c.Upload("/api/v1/documents", "cv.pdf", pdfBytes, "CV").JSON(t, &d)

	r := c.Do(http.MethodPost, "/api/v1/documents/"+d.ID+"/extractions", nil)
	if r.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", r.Code, r.Body)
	}
	var ext struct{ ID, Status string }
	r.JSON(t, &ext)
	if ext.Status != "QUEUED" {
		t.Fatalf("status %s", ext.Status)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM river_job WHERE kind = 'extract_cv'`); n != 1 {
		t.Fatalf("job not enqueued transactionally: %d", n)
	}

	// Run a real River worker against the test database.
	workers := river.NewWorkers()
	river.AddWorker(workers, &documents.ExtractCVWorker{Pool: app.Pool, Store: app.Store, AI: app.AI, Log: app.Log})
	wc, err := jobs.NewWorkerClient(app.Pool, workers, app.Log, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := wc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wc.Stop(context.Background()) }()

	deadline := time.Now().Add(15 * time.Second)
	for {
		c.Do(http.MethodGet, "/api/v1/profile-extractions/"+ext.ID, nil).JSON(t, &ext)
		if ext.Status == "PENDING_REVIEW" || ext.Status == "FAILED" || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if ext.Status != "PENDING_REVIEW" {
		t.Fatalf("extraction status = %s", ext.Status)
	}
	if gotToken != "Bearer svc-token" {
		t.Fatalf("service token not sent: %q", gotToken)
	}
	// Nothing reaches the profile until the user applies the reviewed items.
	if n := app.QueryInt(t, `SELECT count(*) FROM work_experiences`); n != 0 {
		t.Fatal("extraction wrote to the profile without review")
	}

	apply := map[string]any{
		"basics":      map[string]any{"headline": "Backend Engineer", "location_country": "KE", "links": []any{}},
		"experiences": []map[string]any{{"item": map[string]any{"employer": "Acme", "title": "Engineer", "is_current": true}, "verified": true}},
		"skills":      []map[string]any{{"item": map[string]any{"name": "Go"}, "verified": false}},
	}
	r = c.Do(http.MethodPost, "/api/v1/profile-extractions/"+ext.ID+"/apply", apply)
	if r.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", r.Code, r.Body)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM work_experiences WHERE source = 'CV_IMPORT' AND verification = 'VERIFIED'`); n != 1 {
		t.Fatal("confirmed experience not stored as verified CV import")
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM profile_skills WHERE source = 'CV_IMPORT' AND verification = 'UNVERIFIED'`); n != 1 {
		t.Fatal("unconfirmed skill must stay UNVERIFIED")
	}
	// Applying twice is rejected.
	if r := c.Do(http.MethodPost, "/api/v1/profile-extractions/"+ext.ID+"/apply", apply); r.Code != http.StatusConflict {
		t.Fatalf("second apply: %d", r.Code)
	}

	// Another user cannot see the extraction.
	other := app.Client(t)
	other.Register("other@example.com")
	if r := other.Do(http.MethodGet, "/api/v1/profile-extractions/"+ext.ID, nil); r.Code != http.StatusNotFound {
		t.Fatalf("cross-user extraction read: %d", r.Code)
	}
}
