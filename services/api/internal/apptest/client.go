package apptest

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Client is a cookie-aware test client that sends the CSRF token like the SPA.
type Client struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
	CSRF   string
}

func (a *App) Client(t *testing.T) *Client { return &Client{t: t, h: a.Handler} }

type Response struct {
	Code   int
	Body   []byte
	Header http.Header
}

func (r Response) JSON(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
}

// ErrCode returns the error envelope code, or "".
func (r Response) ErrCode() string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.Body, &e)
	return e.Error.Code
}

func (c *Client) Do(method, path string, body any) Response {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req)
}

// DoNoCSRF sends a request without the CSRF header (for negative tests).
func (c *Client) DoNoCSRF(method, path string, body any) Response {
	saved := c.CSRF
	c.CSRF = ""
	defer func() { c.CSRF = saved }()
	return c.Do(method, path, body)
}

func (c *Client) Upload(path, filename string, data []byte, kind string) Response {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if kind != "" {
		_ = mw.WriteField("kind", kind)
	}
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.send(req)
}

func (c *Client) send(req *http.Request) Response {
	req.RemoteAddr = "203.0.113.10:5555"
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.CSRF != "" {
		req.Header.Set("X-CSRF-Token", c.CSRF)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "cp_session" {
			if ck.MaxAge < 0 || ck.Value == "" {
				c.cookie = nil
			} else {
				c.cookie = ck
			}
		}
	}
	var s struct {
		CSRFToken string `json:"csrf_token"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &s) == nil && s.CSRFToken != "" {
		c.CSRF = s.CSRFToken
	}
	return Response{Code: rec.Code, Body: rec.Body.Bytes(), Header: rec.Header()}
}

// Register creates a user and leaves the client signed in.
func (c *Client) Register(email string) {
	c.t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"email": email, "password": "a-strong-passphrase", "display_name": "Test User",
	})
	if r.Code != http.StatusCreated {
		c.t.Fatalf("register %s: %d %s", email, r.Code, r.Body)
	}
}
