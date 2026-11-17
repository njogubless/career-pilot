package outreach

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestComposePlainAndThreaded(t *testing.T) {
	parent := "<p1@careerpilot.app>"
	raw := compose("ada@gmail.test", "jane@acme.test", "Re: Staff role – Go", "Hi Jane,\n\nThanks.", "<m1@careerpilot.app>", &parent, nil, "", time.Unix(0, 0))
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	subj, _ := dec.DecodeHeader(msg.Header.Get("Subject"))
	if subj != "Re: Staff role – Go" || msg.Header.Get("In-Reply-To") != parent || msg.Header.Get("Message-ID") != "<m1@careerpilot.app>" {
		t.Fatalf("headers: %v", msg.Header)
	}
	body, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, msg.Body))
	if string(body) != "Hi Jane,\r\n\r\nThanks." {
		t.Fatalf("body = %q", body)
	}
}

func TestComposeWithAttachment(t *testing.T) {
	raw := compose("a@x.test", "b@y.test", "Hello", "Body", "<m@x>", nil, []byte("PK-docx"), "tailored-cv-v2.docx", time.Now())
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		parts = append(parts, p.FileName()+"|"+string(data))
	}
	if len(parts) != 2 || parts[0] != "|Body" || parts[1] != "tailored-cv-v2.docx|PK-docx" {
		t.Fatalf("parts = %v", parts)
	}
}

func TestNormalizeEmailNeverCompletes(t *testing.T) {
	for in, want := range map[string]string{"Jane@Acme.com": "jane@acme.com", " a.b+c@x.co ": "a.b+c@x.co"} {
		if got, ok := normalizeEmail(in); !ok || got != want {
			t.Errorf("%q → %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"jane", "jane@acme", "Jane <jane@acme.com>", "@acme.com", "jane@@acme.com"} {
		if _, ok := normalizeEmail(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestApprovalHashCoversRecipientContentAndAttachment(t *testing.T) {
	att := uuid.New()
	base := Message{ToEmail: "a@x.test", Subject: "S", Body: "B", AttachmentVersionID: &att}
	h := contentHash(base)
	for name, m := range map[string]Message{
		"to":         {ToEmail: "b@x.test", Subject: "S", Body: "B", AttachmentVersionID: &att},
		"subject":    {ToEmail: "a@x.test", Subject: "S2", Body: "B", AttachmentVersionID: &att},
		"body":       {ToEmail: "a@x.test", Subject: "S", Body: "B!", AttachmentVersionID: &att},
		"attachment": {ToEmail: "a@x.test", Subject: "S", Body: "B"},
	} {
		if bytes.Equal(contentHash(m), h) {
			t.Errorf("changing %s does not invalidate approval", name)
		}
	}
	if !bytes.Equal(contentHash(Message{ToEmail: "A@X.test", Subject: "S", Body: "B", AttachmentVersionID: &att}), h) {
		t.Error("recipient case should not matter")
	}
}

func TestDiscoveryFilters(t *testing.T) {
	for _, e := range []string{"privacy@acme.com", "noreply@acme.com", "accommodations@acme.com", "support@acme.com"} {
		if !nonRecruiting.MatchString(e) {
			t.Errorf("%s should be excluded", e)
		}
	}
	for _, lead := range []string{"If you require a reasonable accommodation to apply for a job, please contact",
		"Suspicious messages can be reported to", "Should you wish to discuss any adjustments to the application process, contact"} {
		if !nonRecruitingCtx.MatchString(lead) {
			t.Errorf("%q should not yield a recruiting contact", lead)
		}
	}
	if nonRecruitingCtx.MatchString("Questions about the role? Email") {
		t.Error("a genuine recruiting sentence was excluded")
	}
	if nonRecruiting.MatchString("talent@acme.com") || !recruitingCtx.MatchString("Send your resume to") {
		t.Fatal("recruiting address or context misclassified")
	}
	if !strings.Contains(assemble("Hi,", []string{"P"}, true, "Best,", "Ada", "sig"), "I've attached my CV") {
		t.Fatal("attachment sentence missing")
	}
}
