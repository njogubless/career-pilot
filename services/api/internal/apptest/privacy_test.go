package apptest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/careerpilot/careerpilot/services/api/internal/privacy"
)

// Every table that references users must be exported or deliberately
// excluded, so a new table can never be silently missing from exports.
func TestExportCoversEveryUserTable(t *testing.T) {
	app := New(t, Options{})
	rows, err := app.Pool.Query(context.Background(), `
		SELECT DISTINCT tc.table_name FROM information_schema.table_constraints tc
		JOIN information_schema.constraint_column_usage ccu ON ccu.constraint_name = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY' AND ccu.table_name = 'users' AND tc.table_schema = current_schema()`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	owned, excluded := privacy.OwnedTables(), privacy.NotExported()
	n := 0
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		n++
		if _, ok := owned[table]; !ok {
			if _, ok := excluded[table]; !ok {
				t.Errorf("table %s references users but is neither exported nor excluded", table)
			}
		}
	}
	if n < 30 {
		t.Fatalf("expected the full schema, saw %d user tables", n)
	}
}

func readZip(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = b
	}
	return files
}

func TestDataExportAndAccountDeletion(t *testing.T) {
	app := New(t, Options{})
	ctx := context.Background()
	alice, bob := app.Client(t), app.Client(t)
	alice.Register("alice.privacy@example.com")
	bob.Register("bob.privacy@example.com")
	var doc struct{ ID string }
	alice.Upload("/api/v1/documents", "My CV.pdf", pdfBytes, "CV").JSON(t, &doc)
	bob.Upload("/api/v1/documents", "bob.pdf", pdfBytes, "CV")
	aliceID := userIDByEmail(t, app, "alice.privacy@example.com")
	var aliceKey string
	if err := app.Pool.QueryRow(ctx, `SELECT storage_key FROM profile_documents WHERE id = $1`, doc.ID).Scan(&aliceKey); err != nil {
		t.Fatal(err)
	}

	// Export needs the password and CSRF.
	if r := alice.Do(http.MethodPost, "/api/v1/users/me/export", map[string]string{"password": "wrong"}); r.Code != 422 {
		t.Fatalf("wrong password: %d", r.Code)
	}
	if r := alice.DoNoCSRF(http.MethodPost, "/api/v1/users/me/export", map[string]string{"password": "a-strong-passphrase"}); r.Code != 403 {
		t.Fatalf("missing CSRF: %d", r.Code)
	}
	r := alice.Do(http.MethodPost, "/api/v1/users/me/export", map[string]string{"password": "a-strong-passphrase"})
	if r.Code != 200 || r.Header.Get("Content-Type") != "application/zip" || !strings.Contains(r.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("export: %d %v", r.Code, r.Header)
	}
	files := readZip(t, r.Body)
	if _, ok := files["README.txt"]; !ok {
		t.Fatal("README missing")
	}
	var data struct {
		Format string                       `json:"format"`
		Tables map[string][]json.RawMessage `json:"tables"`
	}
	if err := json.Unmarshal(files["data.json"], &data); err != nil {
		t.Fatal(err)
	}
	raw := string(files["data.json"])
	if data.Format != privacy.FormatVersion || len(data.Tables["users"]) != 1 || !strings.Contains(raw, "alice.privacy@example.com") {
		t.Fatalf("export content: %s", raw[:min(len(raw), 300)])
	}
	if len(data.Tables["profile_documents"]) != 1 || len(data.Tables["audit_events"]) == 0 {
		t.Fatalf("documents %d audit %d", len(data.Tables["profile_documents"]), len(data.Tables["audit_events"]))
	}
	for _, leak := range []string{"bob.privacy@example.com", "password_hash", "argon2", "storage_key", "token_hash"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("export contains %q", leak)
		}
	}
	var found bool
	for name, b := range files {
		if strings.HasPrefix(name, "documents/"+doc.ID) && bytes.Equal(b, pdfBytes) {
			found = true
		}
	}
	if !found {
		t.Fatalf("uploaded file missing from export: %v", keys(files))
	}

	// Deletion needs the typed confirmation and the password.
	if r := alice.Do(http.MethodPost, "/api/v1/users/me/delete", map[string]string{"password": "a-strong-passphrase", "confirm": "yes"}); r.Code != 422 {
		t.Fatalf("missing confirmation: %d", r.Code)
	}
	if r := alice.Do(http.MethodPost, "/api/v1/users/me/delete", map[string]string{"password": "nope", "confirm": "DELETE"}); r.Code != 422 {
		t.Fatalf("wrong password: %d", r.Code)
	}
	oldCookie := alice.cookie
	if r := alice.Do(http.MethodPost, "/api/v1/users/me/delete", map[string]string{"password": "a-strong-passphrase", "confirm": "DELETE"}); r.Code != 204 {
		t.Fatalf("delete: %d %s", r.Code, r.Body)
	}
	if alice.cookie != nil {
		t.Fatal("session cookie not cleared")
	}
	alice.cookie = oldCookie
	if r := alice.Do(http.MethodGet, "/api/v1/users/me", nil); r.Code != 401 {
		t.Fatalf("old session still valid: %d", r.Code)
	}

	count := func(q string, args ...any) int {
		var n int
		if err := app.Pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(`SELECT count(*) FROM users WHERE id = $1`, aliceID) != 0 || count(`SELECT count(*) FROM profile_documents WHERE user_id = $1`, aliceID) != 0 {
		t.Fatal("rows remain after deletion")
	}
	if app.Store.Has(aliceKey) {
		t.Fatal("stored file remains after deletion")
	}
	// The security record remains, without IP addresses or metadata, plus a
	// deletion event.
	if count(`SELECT count(*) FROM audit_events WHERE user_id = $1 AND action <> 'account.deleted' AND (ip IS NOT NULL OR metadata <> '{}')`, aliceID) != 0 {
		t.Fatal("audit events not pseudonymised")
	}
	if count(`SELECT count(*) FROM audit_events WHERE user_id = $1 AND action = 'account.deleted' AND ip IS NULL`, aliceID) != 1 {
		t.Fatal("deletion not recorded")
	}
	// Bob is untouched; Alice's address can register again.
	if r := bob.Do(http.MethodGet, "/api/v1/documents", nil); r.Code != 200 || !strings.Contains(string(r.Body), "bob.pdf") {
		t.Fatalf("other user affected: %d", r.Code)
	}
	if count(`SELECT count(*) FROM audit_events a JOIN users u ON u.id = a.user_id WHERE u.email = 'bob.privacy@example.com' AND a.ip IS NOT NULL`) == 0 {
		t.Fatal("other user's audit trail changed")
	}
	if r := app.Client(t).Do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "alice.privacy@example.com", "password": "a-strong-passphrase"}); r.Code != 401 {
		t.Fatalf("deleted account can sign in: %d", r.Code)
	}
	app.Client(t).Register("alice.privacy@example.com")
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
