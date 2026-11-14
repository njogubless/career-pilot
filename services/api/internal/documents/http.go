package documents

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
)

type HTTP struct {
	svc    *Service
	authH  *auth.HTTP
	maxLen int64
}

func NewHTTP(svc *Service, authH *auth.HTTP, maxUpload int64) *HTTP {
	return &HTTP{svc: svc, authH: authH, maxLen: maxUpload}
}

// UploadRoutes need a larger body limit than JSON routes; mount separately.
func (h *HTTP) UploadRoutes(r chi.Router) {
	r.Post("/documents", h.upload)
}

// Routes are mounted behind auth.RequireAuth.
func (h *HTTP) Routes(r chi.Router) {
	r.Get("/documents", h.list)
	r.Get("/documents/{id}/download", h.download)
	r.Delete("/documents/{id}", h.delete)
	r.Post("/documents/{id}/extractions", h.startExtraction)
	r.Get("/documents/{id}/extractions/latest", h.latestExtraction)
	r.Get("/profile-extractions/{id}", h.getExtraction)
	r.Post("/profile-extractions/{id}/apply", h.applyExtraction)
	r.Post("/profile-extractions/{id}/discard", h.discardExtraction)
}

func uid(r *http.Request) uuid.UUID { return auth.MustPrincipal(r.Context()).UserID }

func idParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := ids.Parse(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
	}
	return id, ok
}

func (h *HTTP) upload(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"file": "Send the file as multipart/form-data."}))
		return
	}
	kind := "CV"
	var filename string
	var data []byte
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			httpx.WriteError(w, r, uploadErr(err))
			return
		}
		switch part.FormName() {
		case "kind":
			b, _ := io.ReadAll(io.LimitReader(part, 32))
			kind = string(b)
		case "file":
			filename = part.FileName()
			data, err = readLimited(part, h.maxLen)
			if err != nil {
				httpx.WriteError(w, r, uploadErr(err))
				return
			}
		}
		_ = part.Close()
	}
	if data == nil {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"file": "Choose a file to upload."}))
		return
	}
	doc, err := h.svc.Upload(r.Context(), uid(r), kind, filename, data, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, doc)
}

var errTooLarge = errors.New("too large")

func readLimited(p *multipart.Part, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(p, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errTooLarge
	}
	return data, nil
}

func uploadErr(err error) error {
	var maxErr *http.MaxBytesError
	if errors.Is(err, errTooLarge) || errors.As(err, &maxErr) {
		return httpx.NewError(httpx.CodePayloadTooLarge, "The file is too large.")
	}
	return &httpx.Error{Code: httpx.CodeValidationFailed, Message: "The upload could not be read.", Cause: err}
}

func (h *HTTP) list(w http.ResponseWriter, r *http.Request) {
	docs, err := h.svc.List(r.Context(), uid(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": docs})
}

func (h *HTTP) download(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	doc, data, err := h.svc.Download(r.Context(), uid(r), id, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", doc.MimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	// Always download, never render inline: documents are user-supplied content.
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="document"; filename*=UTF-8''%s`, url.PathEscape(doc.OriginalFilename)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *HTTP) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), uid(r), id, h.authH.ClientIP(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) startExtraction(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	ext, err := h.svc.StartExtraction(r.Context(), uid(r), id, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, ext)
}

func (h *HTTP) latestExtraction(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	ext, err := h.svc.LatestExtraction(r.Context(), uid(r), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ext)
}

func (h *HTTP) getExtraction(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	ext, err := h.svc.GetExtraction(r.Context(), uid(r), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ext)
}

func (h *HTTP) applyExtraction(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var batch profiles.ImportBatch
	if err := httpx.DecodeJSON(r, &batch); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.svc.ApplyExtraction(r.Context(), uid(r), id, batch, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *HTTP) discardExtraction(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := h.svc.DiscardExtraction(r.Context(), uid(r), id); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
