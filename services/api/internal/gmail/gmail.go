// Package gmail connects a user's Gmail account through OAuth 2.0
// (authorization code + PKCE, offline access) and exposes the two Gmail
// operations CareerPilot needs: send a message, and read thread metadata to
// detect replies. Scopes are the minimum for that: gmail.send and
// gmail.metadata (headers only; message bodies are never read).
package gmail

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/crypto"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
)

const (
	ScopeSend     = "https://www.googleapis.com/auth/gmail.send"
	ScopeMetadata = "https://www.googleapis.com/auth/gmail.metadata"
	stateTTL      = 10 * time.Minute
)

var Scopes = []string{ScopeSend, ScopeMetadata}

type Config struct {
	ClientID, ClientSecret, RedirectURL string
	AuthURL, TokenURL, RevokeURL        string
	APIBase                             string
}

type Service struct {
	pool   *pgxpool.Pool
	cfg    Config
	sealer *crypto.Sealer
	http   *http.Client
	log    *slog.Logger
	now    func() time.Time
}

// New returns nil when Gmail is not configured; callers report that honestly.
func New(pool *pgxpool.Pool, cfg Config, key []byte, log *slog.Logger) (*Service, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || len(key) != 32 {
		return nil, nil
	}
	sealer, err := crypto.NewSealer(key)
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, cfg: cfg, sealer: sealer, http: &http.Client{Timeout: 20 * time.Second}, log: log, now: time.Now}, nil
}

var (
	ErrNotConnected   = httpx.NewError(httpx.CodeInvalidTransition, "Connect your Gmail account first (Settings → Gmail).")
	ErrNeedsReconnect = httpx.NewError(httpx.CodeInvalidTransition, "Gmail access has expired or was revoked. Reconnect your Gmail account in Settings.")
)

type Account struct {
	Email       string    `json:"email"`
	Scopes      []string  `json:"scopes"`
	Status      string    `json:"status"`
	LastError   *string   `json:"last_error"`
	ConnectedAt time.Time `json:"connected_at"`
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hash(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// StartConnect creates a state + PKCE verifier and returns Google's consent URL.
func (s *Service) StartConnect(ctx context.Context, userID uuid.UUID) (string, error) {
	state, verifier := randomString(32), randomString(48)
	sealed, err := s.sealer.Seal([]byte(verifier), userID[:])
	if err != nil {
		return "", err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM oauth_states WHERE expires_at < now() OR user_id = $1`, userID); err != nil {
		return "", err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO oauth_states (state_hash, user_id, verifier_enc, expires_at) VALUES ($1, $2, $3, $4)`,
		hash(state), userID, sealed, s.now().Add(stateTTL)); err != nil {
		return "", err
	}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":              {s.cfg.ClientID},
		"redirect_uri":           {s.cfg.RedirectURL},
		"response_type":          {"code"},
		"scope":                  {strings.Join(Scopes, " ")},
		"access_type":            {"offline"},
		"prompt":                 {"consent"},
		"include_granted_scopes": {"false"},
		"state":                  {state},
		"code_challenge":         {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method":  {"S256"},
	}
	return s.cfg.AuthURL + "?" + q.Encode(), nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func (s *Service) tokenRequest(ctx context.Context, form url.Values) (tokenResponse, int, error) {
	form.Set("client_id", s.cfg.ClientID)
	form.Set("client_secret", s.cfg.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return tokenResponse{}, 0, err
	}
	defer resp.Body.Close()
	var t tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&t); err != nil {
		return tokenResponse{}, resp.StatusCode, fmt.Errorf("decode token response: %w", err)
	}
	return t, resp.StatusCode, nil
}

// Callback completes the OAuth flow for the signed-in user.
func (s *Service) Callback(ctx context.Context, userID uuid.UUID, state, code, ip string) (Account, error) {
	fail := func(msg string, cause error) (Account, error) {
		_ = audit.Record(ctx, s.pool, audit.Event{UserID: &userID, Action: "integration.gmail.connect", SubjectType: "integration",
			Outcome: audit.Failure, IP: ip, Metadata: map[string]any{"reason": msg}})
		return Account{}, &httpx.Error{Code: httpx.CodeValidationFailed, Message: msg, Cause: cause}
	}
	var owner uuid.UUID
	var sealed []byte
	var expires time.Time
	err := s.pool.QueryRow(ctx, `DELETE FROM oauth_states WHERE state_hash = $1 RETURNING user_id, verifier_enc, expires_at`, hash(state)).
		Scan(&owner, &sealed, &expires)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (owner != userID || s.now().After(expires))) {
		return fail("The connection request expired or is invalid. Start again from Settings.", err)
	}
	if err != nil {
		return Account{}, err
	}
	verifier, err := s.sealer.Open(sealed, userID[:])
	if err != nil {
		return Account{}, err
	}
	t, status, err := s.tokenRequest(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {s.cfg.RedirectURL}, "code_verifier": {string(verifier)}})
	if err != nil || status != http.StatusOK || t.AccessToken == "" {
		return fail("Google did not complete the connection. Please try again.", fmt.Errorf("token exchange: status %d %s %v", status, t.Error, err))
	}
	if t.RefreshToken == "" {
		return fail("Google did not grant offline access. Remove CareerPilot from your Google account permissions and connect again.", nil)
	}
	granted := strings.Fields(t.Scope)
	for _, need := range Scopes {
		if !slices.Contains(granted, need) {
			return fail("Both requested Gmail permissions (send, and read message headers to detect replies) are needed. Please allow both.", nil)
		}
	}
	email, err := s.profileEmail(ctx, t.AccessToken)
	if err != nil {
		return fail("Could not read your Gmail address.", err)
	}
	accEnc, err := s.sealer.Seal([]byte(t.AccessToken), userID[:])
	if err != nil {
		return Account{}, err
	}
	refEnc, err := s.sealer.Seal([]byte(t.RefreshToken), userID[:])
	if err != nil {
		return Account{}, err
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO email_accounts (user_id, provider, email, scopes, access_token_enc, refresh_token_enc,
				access_expires_at, status) VALUES ($1, 'GMAIL', $2, $3, $4, $5, $6, 'CONNECTED')
			ON CONFLICT (user_id) DO UPDATE SET email = EXCLUDED.email, scopes = EXCLUDED.scopes, access_token_enc = EXCLUDED.access_token_enc,
				refresh_token_enc = EXCLUDED.refresh_token_enc, access_expires_at = EXCLUDED.access_expires_at, status = 'CONNECTED',
				last_error = NULL, connected_at = now(), updated_at = now()`,
			userID, email, granted, accEnc, refEnc, s.now().Add(time.Duration(t.ExpiresIn)*time.Second)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "integration.gmail.connect", SubjectType: "integration",
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"scopes": granted}})
	})
	if err != nil {
		return Account{}, err
	}
	acc, err := s.Account(ctx, userID)
	if err != nil || acc == nil {
		return Account{}, err
	}
	return *acc, nil
}

func (s *Service) Account(ctx context.Context, userID uuid.UUID) (*Account, error) {
	var a Account
	err := s.pool.QueryRow(ctx, `SELECT email, scopes, status, last_error, connected_at FROM email_accounts WHERE user_id = $1`, userID).
		Scan(&a.Email, &a.Scopes, &a.Status, &a.LastError, &a.ConnectedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &a, err
}

// Disconnect revokes the grant at Google (best effort) and deletes the tokens.
func (s *Service) Disconnect(ctx context.Context, userID uuid.UUID, ip string) error {
	var refEnc []byte
	err := s.pool.QueryRow(ctx, `DELETE FROM email_accounts WHERE user_id = $1 RETURNING refresh_token_enc`, userID).Scan(&refEnc)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewError(httpx.CodeNotFound, "No Gmail account is connected.")
	}
	if err != nil {
		return err
	}
	revoked := false
	if ref, err := s.sealer.Open(refEnc, userID[:]); err == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.RevokeURL, strings.NewReader(url.Values{"token": {string(ref)}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if resp, err := s.http.Do(req); err == nil {
			revoked = resp.StatusCode == http.StatusOK
			resp.Body.Close()
		}
	}
	return audit.Record(ctx, s.pool, audit.Event{UserID: &userID, Action: "integration.gmail.disconnect", SubjectType: "integration",
		Outcome: audit.Success, IP: ip, Metadata: map[string]any{"revoked_at_google": revoked}})
}

// token returns a valid access token and the account address, refreshing
// when needed. A rejected refresh token marks the account NEEDS_RECONNECT.
func (s *Service) token(ctx context.Context, userID uuid.UUID) (string, string, error) {
	var email, status string
	var accEnc, refEnc []byte
	var expires time.Time
	err := s.pool.QueryRow(ctx, `SELECT email, status, access_token_enc, refresh_token_enc, access_expires_at FROM email_accounts WHERE user_id = $1`,
		userID).Scan(&email, &status, &accEnc, &refEnc, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotConnected
	}
	if err != nil {
		return "", "", err
	}
	if status != "CONNECTED" {
		return "", "", ErrNeedsReconnect
	}
	if s.now().Before(expires.Add(-time.Minute)) {
		acc, err := s.sealer.Open(accEnc, userID[:])
		return string(acc), email, err
	}
	ref, err := s.sealer.Open(refEnc, userID[:])
	if err != nil {
		return "", "", err
	}
	t, code, err := s.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {string(ref)}})
	if err != nil {
		return "", "", fmt.Errorf("refresh token: %w", err)
	}
	if code == http.StatusBadRequest || code == http.StatusUnauthorized || t.Error == "invalid_grant" {
		_, _ = s.pool.Exec(ctx, `UPDATE email_accounts SET status = 'NEEDS_RECONNECT', last_error = 'Google rejected the stored grant.', updated_at = now()
			WHERE user_id = $1`, userID)
		return "", "", ErrNeedsReconnect
	}
	if code != http.StatusOK || t.AccessToken == "" {
		return "", "", fmt.Errorf("refresh token: status %d", code)
	}
	newAcc, err := s.sealer.Seal([]byte(t.AccessToken), userID[:])
	if err != nil {
		return "", "", err
	}
	newRef := refEnc
	if t.RefreshToken != "" { // rotation
		if newRef, err = s.sealer.Seal([]byte(t.RefreshToken), userID[:]); err != nil {
			return "", "", err
		}
	}
	if _, err := s.pool.Exec(ctx, `UPDATE email_accounts SET access_token_enc = $2, refresh_token_enc = $3, access_expires_at = $4, updated_at = now()
		WHERE user_id = $1`, userID, newAcc, newRef, s.now().Add(time.Duration(t.ExpiresIn)*time.Second)); err != nil {
		return "", "", err
	}
	return t.AccessToken, email, nil
}

// APIError is a Gmail API failure. Definite reports whether Gmail certainly
// did not perform the operation (4xx); 5xx and network errors are uncertain.
type APIError struct {
	Status   int
	Message  string
	Definite bool
}

func (e *APIError) Error() string {
	return fmt.Sprintf("gmail api: status %d: %s", e.Status, e.Message)
}

func (s *Service) api(ctx context.Context, token, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, s.cfg.APIBase+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return &APIError{Status: 0, Message: "network error", Definite: false}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var env struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		return &APIError{Status: resp.StatusCode, Message: env.Error.Message, Definite: resp.StatusCode >= 400 && resp.StatusCode < 500}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (s *Service) profileEmail(ctx context.Context, token string) (string, error) {
	var p struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := s.api(ctx, token, http.MethodGet, "/gmail/v1/users/me/profile", nil, &p); err != nil {
		return "", err
	}
	if p.EmailAddress == "" {
		return "", errors.New("empty profile address")
	}
	return p.EmailAddress, nil
}

// From returns the connected address (for composing the From header).
func (s *Service) From(ctx context.Context, userID uuid.UUID) (string, error) {
	_, email, err := s.token(ctx, userID)
	return email, err
}

// Send sends a raw RFC 5322 message, optionally into an existing thread.
func (s *Service) Send(ctx context.Context, userID uuid.UUID, raw []byte, threadID string) (string, string, error) {
	tok, _, err := s.token(ctx, userID)
	if err != nil {
		return "", "", err
	}
	payload := map[string]string{"raw": base64.RawURLEncoding.EncodeToString(raw)}
	if threadID != "" {
		payload["threadId"] = threadID
	}
	b, _ := json.Marshal(payload)
	var out struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	if err := s.api(ctx, tok, http.MethodPost, "/gmail/v1/users/me/messages/send", strings.NewReader(string(b)), &out); err != nil {
		return "", "", err
	}
	return out.ID, out.ThreadID, nil
}

type ThreadMessage struct {
	ID         string
	From       string
	ReceivedAt time.Time
	LabelIDs   []string
}

// Thread returns header metadata of a thread's messages (no bodies).
func (s *Service) Thread(ctx context.Context, userID uuid.UUID, threadID string) ([]ThreadMessage, string, error) {
	tok, email, err := s.token(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Messages []struct {
			ID           string   `json:"id"`
			InternalDate string   `json:"internalDate"`
			LabelIDs     []string `json:"labelIds"`
			Payload      struct {
				Headers []struct{ Name, Value string } `json:"headers"`
			} `json:"payload"`
		} `json:"messages"`
	}
	path := "/gmail/v1/users/me/threads/" + url.PathEscape(threadID) + "?format=metadata&metadataHeaders=From"
	if err := s.api(ctx, tok, http.MethodGet, path, nil, &out); err != nil {
		return nil, "", err
	}
	msgs := make([]ThreadMessage, 0, len(out.Messages))
	for _, m := range out.Messages {
		ms, _ := strconv.ParseInt(m.InternalDate, 10, 64)
		tm := ThreadMessage{ID: m.ID, ReceivedAt: time.UnixMilli(ms).UTC(), LabelIDs: m.LabelIDs}
		for _, h := range m.Payload.Headers {
			if strings.EqualFold(h.Name, "From") {
				tm.From = h.Value
			}
		}
		msgs = append(msgs, tm)
	}
	return msgs, email, nil
}
