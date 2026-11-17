package apptest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/careerpilot/careerpilot/services/api/internal/sources"
)

// fakeGoogle implements the parts of Google OAuth and the Gmail API we use.
type fakeGoogle struct {
	mu          sync.Mutex
	challenge   string
	refreshFail bool
	sendStatus  int // 0 = OK
	sent        []string
	revoked     int
	threads     map[string][]map[string]any
	n           int
	accessToken string
}

func newFakeGoogle() *fakeGoogle {
	return &fakeGoogle{threads: map[string][]map[string]any{}, accessToken: "access-1"}
}

func (g *fakeGoogle) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		g.mu.Lock()
		defer g.mu.Unlock()
		if r.Form.Get("client_secret") != "test-secret" {
			w.WriteHeader(401)
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
				w.WriteHeader(400)
				writeJSON(w, map[string]string{"error": "invalid_grant"})
				return
			}
			writeJSON(w, map[string]any{"access_token": g.accessToken, "refresh_token": "refresh-1", "expires_in": 3600,
				"scope": "https://www.googleapis.com/auth/gmail.send https://www.googleapis.com/auth/gmail.metadata"})
		case "refresh_token":
			if g.refreshFail {
				w.WriteHeader(400)
				writeJSON(w, map[string]string{"error": "invalid_grant"})
				return
			}
			g.accessToken = "access-2"
			writeJSON(w, map[string]any{"access_token": g.accessToken, "expires_in": 3600})
		}
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.revoked++
		g.mu.Unlock()
	})
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+g.accessToken {
			w.WriteHeader(401)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /gmail/v1/users/me/profile", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			writeJSON(w, map[string]string{"emailAddress": "ada@gmail.test"})
		}
	})
	mux.HandleFunc("POST /gmail/v1/users/me/messages/send", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var in struct{ Raw, ThreadID string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.sendStatus != 0 {
			w.WriteHeader(g.sendStatus)
			writeJSON(w, map[string]any{"error": map[string]string{"message": "simulated"}})
			return
		}
		raw, _ := base64.RawURLEncoding.DecodeString(in.Raw)
		g.sent = append(g.sent, string(raw))
		g.n++
		id := fmt.Sprintf("gm-%d", g.n)
		thread := in.ThreadID
		if thread == "" {
			thread = "th-" + id
		}
		g.threads[thread] = append(g.threads[thread], map[string]any{"id": id, "internalDate": fmt.Sprint(time.Now().UnixMilli()),
			"labelIds": []string{"SENT"}, "payload": map[string]any{"headers": []map[string]string{{"name": "From", "value": "Ada <ada@gmail.test>"}}}})
		writeJSON(w, map[string]string{"id": id, "threadId": thread})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/threads/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.URL.Query().Get("format") != "metadata" {
			w.WriteHeader(403) // the metadata scope cannot read bodies
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		writeJSON(w, map[string]any{"messages": g.threads[r.PathValue("id")]})
	})
	return mux
}

func (g *fakeGoogle) reply(thread, id, from string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.threads[thread] = append(g.threads[thread], map[string]any{"id": id, "internalDate": fmt.Sprint(time.Now().Add(time.Second).UnixMilli()),
		"labelIds": []string{"INBOX"}, "payload": map[string]any{"headers": []map[string]string{{"name": "From", "value": from}}}})
}

type fakeResolver struct{}

func (fakeResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	switch name {
	case "acme-corp.test", "bounce.test", "other.test":
		return []*net.MX{{Host: "mx." + name, Pref: 10}}, nil
	case "nullmx.test":
		return []*net.MX{{Host: ".", Pref: 0}}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

type outMsg struct {
	ID            string
	Status        string
	Kind          string
	Body          string
	ApplicationID *string `json:"application_id"`
	ParentID      *string `json:"parent_id"`
	GmailThreadID *string `json:"gmail_thread_id"`
	RepliedAt     *string `json:"replied_at"`
	ErrorMessage  *string `json:"error_message"`
	FollowUps     []struct {
		Sequence        int
		Status          string
		CancelledReason *string `json:"cancelled_reason"`
	} `json:"follow_ups"`
	Replies []struct {
		IsBounce      bool    `json:"is_bounce"`
		ApplicationID *string `json:"application_id"`
	}
}

type contactOut struct {
	ID                string
	Email             *string
	Source            string
	EmailVerification string `json:"email_verification"`
	Confidence        string
}

func setupOutreach(t *testing.T) (*App, *fakeGoogle, *Client, string) {
	t.Helper()
	google := newFakeGoogle()
	gs := httptest.NewServer(google.handler())
	t.Cleanup(gs.Close)
	docAI := httptest.NewServer((&fakeDocAI{llm: true}).handler())
	t.Cleanup(docAI.Close)
	gh := newFake(sources.Greenhouse)
	app := New(t, Options{AIURL: docAI.URL, Adapters: sources.Registry{sources.Greenhouse: gh}, GoogleURL: gs.URL, Resolver: fakeResolver{}})
	src := addSource(t, app, sources.Greenhouse, "acme", 15)
	gh.set("acme", withText(posting("a", "Staff Engineer", "Acme", "Remote - Worldwide"),
		"Requirements:\nGo\nQuestions about the role? Email recruiting@acme-corp.test. Privacy requests: privacy@acme-corp.test."))
	if run, err := app.Discovery.RunSource(context.Background(), src, "MANUAL"); err != nil || run.Created != 1 {
		t.Fatalf("discovery: %+v %v", run, err)
	}
	var job string
	_ = app.Pool.QueryRow(context.Background(), `SELECT id::text FROM jobs`).Scan(&job)
	c := app.Client(t)
	c.Register("outreach@example.com")
	if r := c.Do(http.MethodPost, "/api/v1/profiles/me/experiences", map[string]any{"employer": "Acme", "title": "Engineer",
		"start_date": "2020-01-01", "is_current": true, "achievements": []string{"Shipped the billing API."}}); r.Code >= 300 {
		t.Fatalf("experience: %d", r.Code)
	}
	if r := c.Do(http.MethodPut, "/api/v1/profiles/me/basics", map[string]any{"headline": "Backend engineer", "summary": "",
		"location_city": "", "links": []any{}}); r.Code >= 300 {
		t.Fatalf("basics: %d %s", r.Code, r.Body)
	}
	return app, google, c, job
}

func connectGmail(t *testing.T, c *Client, google *fakeGoogle) {
	t.Helper()
	var start struct {
		AuthURL string `json:"auth_url"`
	}
	c.Do(http.MethodPost, "/api/v1/integrations/gmail/connect", nil).JSON(t, &start)
	u, err := url.Parse(start.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" ||
		q.Get("scope") != "https://www.googleapis.com/auth/gmail.send https://www.googleapis.com/auth/gmail.metadata" {
		t.Fatalf("auth url: %s", start.AuthURL)
	}
	google.mu.Lock()
	google.challenge = q.Get("code_challenge")
	google.mu.Unlock()
	r := c.Do(http.MethodGet, "/api/v1/integrations/gmail/callback?state="+url.QueryEscape(q.Get("state"))+"&code=good-code", nil)
	if r.Code != http.StatusFound || !strings.Contains(r.Header.Get("Location"), "gmail=connected") {
		t.Fatalf("callback: %d %s", r.Code, r.Header.Get("Location"))
	}
}

func sendKey(c *Client, id, key string) Response {
	return c.DoWithHeader(http.MethodPost, "/api/v1/outreach/messages/"+id+"/send", nil, map[string]string{"Idempotency-Key": key})
}

func draftAndApprove(t *testing.T, c *Client, body map[string]any) outMsg {
	t.Helper()
	var m outMsg
	r := c.Do(http.MethodPost, "/api/v1/outreach/messages", body)
	if r.Code != http.StatusCreated {
		t.Fatalf("draft: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &m)
	if r := c.Do(http.MethodPost, "/api/v1/outreach/messages/"+m.ID+"/approve", map[string]any{"confirm": true}); r.Code != 200 {
		t.Fatalf("approve: %d %s", r.Code, r.Body)
	}
	return m
}

func TestGmailOAuthAndTokens(t *testing.T) {
	app, google, c, _ := setupOutreach(t)
	var st struct {
		Configured bool
		Account    *struct{ Email, Status string }
	}
	c.Do(http.MethodGet, "/api/v1/integrations/gmail", nil).JSON(t, &st)
	if !st.Configured || st.Account != nil {
		t.Fatalf("initial: %+v", st)
	}
	// A forged or replayed state is refused.
	r := c.Do(http.MethodGet, "/api/v1/integrations/gmail/callback?state=forged&code=good-code", nil)
	if !strings.Contains(r.Header.Get("Location"), "gmail=error") {
		t.Fatalf("forged state: %s", r.Header.Get("Location"))
	}
	connectGmail(t, c, google)
	c.Do(http.MethodGet, "/api/v1/integrations/gmail", nil).JSON(t, &st)
	if st.Account == nil || st.Account.Email != "ada@gmail.test" || st.Account.Status != "CONNECTED" {
		t.Fatalf("connected: %+v", st)
	}
	// Tokens are encrypted at rest.
	if n := app.QueryInt(t, `SELECT count(*) FROM email_accounts WHERE position('access-1'::bytea in access_token_enc) > 0
		OR position('refresh-1'::bytea in refresh_token_enc) > 0`); n != 0 {
		t.Fatal("tokens stored in plaintext")
	}
	// Another user's state cannot complete this user's flow.
	other := app.Client(t)
	other.Register("oauth-other@example.com")
	var start struct {
		AuthURL string `json:"auth_url"`
	}
	c.Do(http.MethodPost, "/api/v1/integrations/gmail/connect", nil).JSON(t, &start)
	u, _ := url.Parse(start.AuthURL)
	r = other.Do(http.MethodGet, "/api/v1/integrations/gmail/callback?state="+url.QueryEscape(u.Query().Get("state"))+"&code=good-code", nil)
	if !strings.Contains(r.Header.Get("Location"), "gmail=error") {
		t.Fatalf("cross-user state accepted: %s", r.Header.Get("Location"))
	}
	// An expired access token is refreshed; a revoked grant needs reconnecting.
	app.Exec(t, `UPDATE email_accounts SET access_expires_at = now() - interval '1 hour'`)
	google.mu.Lock()
	google.refreshFail = true
	google.mu.Unlock()
	if r := c.Do(http.MethodPost, "/api/v1/outreach/poll", nil); r.Code != 409 || !strings.Contains(string(r.Body), "Reconnect") {
		t.Fatalf("revoked grant: %d %s", r.Code, r.Body)
	}
	c.Do(http.MethodGet, "/api/v1/integrations/gmail", nil).JSON(t, &st)
	if st.Account.Status != "NEEDS_RECONNECT" {
		t.Fatalf("status: %+v", st.Account)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/integrations/gmail", nil); r.Code != 204 {
		t.Fatalf("disconnect: %d", r.Code)
	}
	if google.revoked != 1 || app.QueryInt(t, `SELECT count(*) FROM email_accounts`) != 0 {
		t.Fatal("disconnect did not revoke and delete tokens")
	}
}

func TestOutreachContactsAndSending(t *testing.T) {
	app, google, c, job := setupOutreach(t)
	ctx := context.Background()

	// Discovery records only addresses the posting publishes, and skips non-recruiting ones.
	var found struct {
		Data []struct{ Contact contactOut }
	}
	c.Do(http.MethodPost, "/api/v1/jobs/"+job+"/contacts/discover", nil).JSON(t, &found)
	if len(found.Data) != 1 {
		t.Fatalf("discovered: %+v", found)
	}
	rec := found.Data[0].Contact
	if *rec.Email != "recruiting@acme-corp.test" || rec.Source != "JOB_POSTING" || rec.EmailVerification != "DOMAIN_OK" || rec.Confidence != "MEDIUM" {
		t.Fatalf("contact: %+v", rec)
	}
	var bad contactOut
	c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"name": "Nobody", "email": "x@nullmx.test"}).JSON(t, &bad)
	if bad.EmailVerification != "INVALID" {
		t.Fatalf("null MX: %+v", bad)
	}
	if r := c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"name": "Jane", "email": "jane"}); r.Code != 422 {
		t.Fatalf("invalid email accepted: %d", r.Code)
	}
	if r := c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"name": "Dup", "email": "Recruiting@acme-corp.test"}); r.Code != 422 {
		t.Fatalf("duplicate email: %d", r.Code)
	}

	// Track the application so outreach associates with it.
	var app1 appDetail
	c.Do(http.MethodPost, "/api/v1/applications", map[string]any{"job_id": job}).JSON(t, &app1)
	cvDoc := approvedCV(t, c, job)
	var doc docView
	c.Do(http.MethodGet, "/api/v1/application-documents/"+cvDoc, nil).JSON(t, &doc)

	// Template draft (no AI), with the approved CV attached.
	var m outMsg
	r := c.Do(http.MethodPost, "/api/v1/outreach/messages", map[string]any{"contact_id": rec.ID, "job_id": job,
		"mode": "TEMPLATE", "attachment_version_id": doc.Current.ID})
	if r.Code != 201 {
		t.Fatalf("draft: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &m)
	if m.Status != "DRAFT" || !strings.HasPrefix(m.Body, "Hello,") || !strings.Contains(m.Body, "Staff Engineer role at Acme") ||
		!strings.Contains(m.Body, "attached my CV") || m.ApplicationID == nil || *m.ApplicationID != app1.ID {
		t.Fatalf("draft: %+v", m)
	}
	path := "/api/v1/outreach/messages/" + m.ID
	// Sending needs a connected Gmail; approval needs consent and is bound to the content.
	if r := sendKey(c, m.ID, "send-key-0001"); r.Code != 409 || !strings.Contains(string(r.Body), "Connect your Gmail") {
		t.Fatalf("send before connect: %d %s", r.Code, r.Body)
	}
	connectGmail(t, c, google)
	if r := sendKey(c, m.ID, "send-key-0001"); r.Code != 409 {
		t.Fatalf("send draft: %d", r.Code)
	}
	if r := c.Do(http.MethodPost, path+"/approve", map[string]any{}); r.Code != 422 {
		t.Fatalf("approve without consent: %d", r.Code)
	}
	c.Do(http.MethodPost, path+"/approve", map[string]any{"confirm": true})
	edited := strings.Replace(m.Body, "Hello,", "Hello Acme team,", 1)
	var after outMsg
	c.Do(http.MethodPut, path, map[string]any{"subject": "Staff Engineer role", "body": edited, "attachment_version_id": doc.Current.ID}).JSON(t, &after)
	if after.Status != "DRAFT" {
		t.Fatalf("editing must withdraw approval: %s", after.Status)
	}
	c.Do(http.MethodPost, path+"/approve", map[string]any{"confirm": true})

	r = sendKey(c, m.ID, "send-key-0001")
	if r.Code != 200 {
		t.Fatalf("send: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &m)
	if m.Status != "SENT" || m.GmailThreadID == nil || len(m.FollowUps) != 1 || m.FollowUps[0].Status != "SCHEDULED" {
		t.Fatalf("sent: %+v", m)
	}
	raw := google.sent[0]
	for _, want := range []string{"To: recruiting@acme-corp.test", "From: ada@gmail.test", "Content-Disposition: attachment; filename=\"tailored-cv-acme", "Message-ID: <"} {
		if !strings.Contains(raw, want) {
			t.Errorf("raw message missing %q", want)
		}
	}
	// Replays never send twice.
	if r := sendKey(c, m.ID, "send-key-0001"); r.Code != 200 || len(google.sent) != 1 {
		t.Fatalf("replay: %d sent=%d", r.Code, len(google.sent))
	}
	if r := sendKey(c, m.ID, "send-key-0002"); r.Code != 409 {
		t.Fatalf("second key on a sent message: %d", r.Code)
	}
	// No second introduction without a reply (and the cooldown applies).
	second := draftAndApprove(t, c, map[string]any{"contact_id": rec.ID, "job_id": job})
	if r := sendKey(c, second.ID, "send-key-0003"); r.Code != 409 {
		t.Fatalf("repeat introduction: %d %s", r.Code, r.Body)
	}

	// Reply detection: metadata only, associated with the application, cancels follow-ups.
	google.reply(*m.GmailThreadID, "in-1", "Recruiter <recruiting@acme-corp.test>")
	var st struct {
		Replies int `json:"replies_found"`
	}
	c.Do(http.MethodPost, "/api/v1/outreach/poll", nil).JSON(t, &st)
	if st.Replies != 1 {
		t.Fatalf("poll: %+v", st)
	}
	c.Do(http.MethodGet, path, nil).JSON(t, &m)
	if m.RepliedAt == nil || len(m.Replies) != 1 || *m.Replies[0].ApplicationID != app1.ID || m.FollowUps[0].Status != "CANCELLED" ||
		*m.FollowUps[0].CancelledReason != "Reply received" {
		t.Fatalf("after reply: %+v", m)
	}
	if _, err := app.Outreach.PollUser(ctx, userIDByEmail(t, app, "outreach@example.com")); err != nil {
		t.Fatal(err)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM outreach_replies`); n != 1 {
		t.Fatalf("duplicate replies: %d", n)
	}

	// Isolation.
	other := app.Client(t)
	other.Register("outreach-other@example.com")
	if r := other.Do(http.MethodGet, path, nil); r.Code != 404 {
		t.Fatalf("cross-user read: %d", r.Code)
	}
	if r := other.Do(http.MethodGet, "/api/v1/contacts/"+rec.ID, nil); r.Code != 404 {
		t.Fatalf("cross-user contact: %d", r.Code)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM audit_events WHERE action = 'outreach.send' AND outcome = 'SUCCESS'`); n != 1 {
		t.Fatalf("send audit: %d", n)
	}
}

func TestFollowUpsBouncesAndFailures(t *testing.T) {
	app, google, c, job := setupOutreach(t)
	ctx := context.Background()
	connectGmail(t, c, google)
	var contact contactOut
	c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"name": "Jane Doe", "email": "jane@other.test"}).JSON(t, &contact)
	m := draftAndApprove(t, c, map[string]any{"contact_id": contact.ID, "job_id": job})
	if !strings.HasPrefix(m.Body, "Hi Jane,") {
		t.Fatalf("greeting: %q", m.Body)
	}
	if r := sendKey(c, m.ID, "fu-send-0001"); r.Code != 200 {
		t.Fatalf("send: %d %s", r.Code, r.Body)
	}

	// Follow-ups become drafts only when due, and wait while automation is paused.
	app.Exec(t, `UPDATE outreach_follow_ups SET due_at = now() - interval '1 minute'`)
	c.Do(http.MethodPost, "/api/v1/automation/pause", nil)
	if n, err := app.Outreach.RunFollowUps(ctx); err != nil || n != 0 {
		t.Fatalf("paused run: %d %v", n, err)
	}
	c.Do(http.MethodPost, "/api/v1/automation/resume", nil)
	if n, err := app.Outreach.RunFollowUps(ctx); err != nil || n != 1 {
		t.Fatalf("run: %d %v", n, err)
	}
	var drafts struct{ Data []outMsg }
	c.Do(http.MethodGet, "/api/v1/outreach/messages?status=DRAFT", nil).JSON(t, &drafts)
	if len(drafts.Data) != 1 || drafts.Data[0].Kind != "FOLLOW_UP" || *drafts.Data[0].ParentID != m.ID {
		t.Fatalf("follow-up draft: %+v", drafts)
	}
	fu := drafts.Data[0]
	c.Do(http.MethodPost, "/api/v1/outreach/messages/"+fu.ID+"/approve", map[string]any{"confirm": true})
	// The recipient cooldown (3 days by default) applies to follow-ups too.
	if r := sendKey(c, fu.ID, "fu-send-0002"); r.Code != 409 || !strings.Contains(string(r.Body), "cooldown") {
		t.Fatalf("cooldown: %d %s", r.Code, r.Body)
	}
	app.Exec(t, `UPDATE outreach_messages SET sent_at = now() - interval '6 days' WHERE status = 'SENT'`)
	var sent outMsg
	if r := sendKey(c, fu.ID, "fu-send-0002"); r.Code != 200 {
		t.Fatalf("follow-up send: %d %s", r.Code, r.Body)
	} else {
		r.JSON(t, &sent)
	}
	if sent.Status != "SENT" || *sent.GmailThreadID != "th-gm-1" || !strings.Contains(google.sent[1], "In-Reply-To: <") ||
		!strings.Contains(google.sent[1], "Subject: Re: ") {
		t.Fatalf("follow-up not threaded: %+v", sent)
	}

	// Do-not-contact blocks sending and cancels follow-ups.
	var dnc contactOut
	c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"name": "Sam", "email": "sam@acme-corp.test"}).JSON(t, &dnc)
	m2 := draftAndApprove(t, c, map[string]any{"contact_id": dnc.ID, "job_id": job})
	c.Do(http.MethodPut, "/api/v1/contacts/"+dnc.ID, map[string]any{"name": "Sam", "email": "sam@acme-corp.test", "do_not_contact": true,
		"do_not_contact_reason": "Asked not to be contacted"})
	if r := sendKey(c, m2.ID, "dnc-send-0001"); r.Code != 409 || !strings.Contains(string(r.Body), "do-not-contact") {
		t.Fatalf("do-not-contact: %d %s", r.Code, r.Body)
	}

	// Bounces mark the address and block further sends.
	var b contactOut
	c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"email": "ghost@bounce.test"}).JSON(t, &b)
	m3 := draftAndApprove(t, c, map[string]any{"contact_id": b.ID})
	var m3s outMsg
	sendKey(c, m3.ID, "bounce-send-0001").JSON(t, &m3s)
	google.reply(*m3s.GmailThreadID, "bounce-1", "Mail Delivery Subsystem <mailer-daemon@googlemail.com>")
	app.Exec(t, `DELETE FROM outreach_messages WHERE id = $1`, m2.ID) // not needed further
	if _, err := app.Outreach.PollUser(ctx, userIDByEmail(t, app, "outreach@example.com")); err != nil {
		t.Fatal(err)
	}
	var bAfter contactOut
	c.Do(http.MethodGet, "/api/v1/contacts/"+b.ID, nil).JSON(t, &bAfter)
	if bAfter.EmailVerification != "BOUNCED" {
		t.Fatalf("bounce not recorded: %+v", bAfter)
	}

	// Gmail failures: 4xx is FAILED (not sent); 5xx is UNKNOWN (never retried automatically).
	var x contactOut
	c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"email": "x@acme-corp.test"}).JSON(t, &x)
	m4 := draftAndApprove(t, c, map[string]any{"contact_id": x.ID})
	google.mu.Lock()
	google.sendStatus = 500
	google.mu.Unlock()
	var m4s outMsg
	sendKey(c, m4.ID, "fail-send-0001").JSON(t, &m4s)
	if m4s.Status != "UNKNOWN" {
		t.Fatalf("5xx: %+v", m4s)
	}
	if r := c.Do(http.MethodPost, "/api/v1/outreach/messages/"+m4.ID+"/mark-not-sent", nil); r.Code != 200 {
		t.Fatalf("mark not sent: %d", r.Code)
	}
	var y contactOut
	c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"email": "y@acme-corp.test"}).JSON(t, &y)
	m5 := draftAndApprove(t, c, map[string]any{"contact_id": y.ID})
	google.mu.Lock()
	google.sendStatus = 400
	google.mu.Unlock()
	var m5s outMsg
	sendKey(c, m5.ID, "fail-send-0002").JSON(t, &m5s)
	if m5s.Status != "FAILED" || m5s.ErrorMessage == nil || !strings.Contains(*m5s.ErrorMessage, "simulated") {
		t.Fatalf("4xx: %+v", m5s)
	}

	// Daily limit.
	google.mu.Lock()
	google.sendStatus = 0
	google.mu.Unlock()
	if r := c.Do(http.MethodPut, "/api/v1/outreach/settings", map[string]any{"daily_send_limit": 1, "recipient_cooldown_days": 1,
		"follow_ups_enabled": false, "follow_up_after_days": 5, "max_follow_ups": 1, "signature": ""}); r.Code != 200 {
		t.Fatalf("settings: %d %s", r.Code, r.Body)
	}
	var z contactOut
	c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"email": "z@acme-corp.test"}).JSON(t, &z)
	m6 := draftAndApprove(t, c, map[string]any{"contact_id": z.ID})
	if r := sendKey(c, m6.ID, "limit-send-0001"); r.Code != 429 {
		t.Fatalf("daily limit: %d %s", r.Code, r.Body)
	}
	// Turning follow-ups off cancelled every scheduled one.
	if n := app.QueryInt(t, `SELECT count(*) FROM outreach_follow_ups WHERE status = 'SCHEDULED'`); n != 0 {
		t.Fatalf("scheduled follow-ups remain: %d", n)
	}
}

func TestOutreachAIDraft(t *testing.T) {
	_, _, c, job := setupOutreach(t)
	var contact contactOut
	c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"name": "Priya Raman", "title": "Recruiter", "email": "priya@acme-corp.test", "job_id": job}).JSON(t, &contact)
	var m struct {
		Subject, Body, Origin string
		PromptVersion         *string  `json:"prompt_version"`
		Questions             []string `json:"questions"`
	}
	r := c.Do(http.MethodPost, "/api/v1/outreach/messages", map[string]any{"contact_id": contact.ID, "mode": "AI", "notes": "I use Acme daily."})
	if r.Code != 201 {
		t.Fatalf("ai draft: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &m)
	if m.Origin != "AI" || !strings.HasPrefix(m.Body, "Hi Priya,") || !strings.Contains(m.Body, "Best regards,\nTest User") ||
		*m.PromptVersion != "outreach_email@1" || len(m.Questions) != 1 {
		t.Fatalf("ai draft: %+v", m)
	}
	// Without a contact address nothing can be drafted; addresses are never guessed.
	var noEmail contactOut
	c.Do(http.MethodPost, "/api/v1/contacts", map[string]any{"name": "No Address"}).JSON(t, &noEmail)
	if r := c.Do(http.MethodPost, "/api/v1/outreach/messages", map[string]any{"contact_id": noEmail.ID}); r.Code != 422 ||
		!strings.Contains(string(r.Body), "never guessed") {
		t.Fatalf("draft without email: %d %s", r.Code, r.Body)
	}
}
