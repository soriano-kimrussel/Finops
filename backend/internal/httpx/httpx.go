package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"finopshq/internal/apperr"
)

const maxBodyBytes = 1 << 20

func URLParamUUID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, apperr.NotFound("resource")
	}
	return id, nil
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		WriteJSON(w, ae.Status, map[string]any{"error": ae})
		return
	}
	slog.ErrorContext(r.Context(), "unhandled error", "err", err, "path", r.URL.Path, "request_id", RequestID(r.Context()))
	WriteJSON(w, http.StatusInternalServerError, map[string]any{
		"error": apperr.New(http.StatusInternalServerError, "internal", "something went wrong"),
	})
}

// Handle adapts a function returning (status, body, error) into a handler. A nil body with
// status 204 writes no content.
func Handle(fn func(w http.ResponseWriter, r *http.Request) (int, any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, body, err := fn(w, r)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		WriteJSON(w, status, body)
	}
}

// Decode is a generic DecodeJSON.
func Decode[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var v T
	err := DecodeJSON(w, r, &v)
	return v, err
}

// DecodeJSON reads a single JSON object, rejecting unknown fields and oversized bodies.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return apperr.New(http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return apperr.New(http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
		}
		if errors.Is(err, io.EOF) {
			return apperr.BadRequest("request body is empty")
		}
		return apperr.BadRequest(fmt.Sprintf("invalid JSON: %s", err.Error()))
	}
	if dec.More() {
		return apperr.BadRequest("request body must contain a single JSON object")
	}
	return nil
}
