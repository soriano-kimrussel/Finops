package apperr

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Error struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func New(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

func NotFound(what string) *Error {
	return New(http.StatusNotFound, "not_found", what+" not found")
}

func Unauthorized() *Error {
	return New(http.StatusUnauthorized, "unauthorized", "authentication required")
}

func Forbidden(msg string) *Error {
	if msg == "" {
		msg = "you do not have permission to perform this action"
	}
	return New(http.StatusForbidden, "forbidden", msg)
}

func BadRequest(msg string) *Error { return New(http.StatusBadRequest, "bad_request", msg) }

func Conflict(code, msg string) *Error { return New(http.StatusConflict, code, msg) }

func Unprocessable(code, msg string) *Error {
	return New(http.StatusUnprocessableEntity, code, msg)
}

// Validation collects field errors; call Err() at the end to get nil when there are none.
type Validation struct{ fields map[string]string }

func (v *Validation) Add(field, msg string) {
	if v.fields == nil {
		v.fields = map[string]string{}
	}
	if _, exists := v.fields[field]; !exists {
		v.fields[field] = msg
	}
}

// With is a shorthand for a single-field validation error.
func (v *Validation) With(field, msg string) error {
	v.Add(field, msg)
	return v.Err()
}

func (v *Validation) Has(field string) bool { _, ok := v.fields[field]; return ok }

func (v *Validation) Err() error {
	if len(v.fields) == 0 {
		return nil
	}
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "some fields are invalid", Fields: v.fields}
}

func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// ConstraintViolation reports the name of the violated unique/check/FK constraint, if any.
func ConstraintViolation(err error) (code, constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName != "" {
		return pgErr.Code, pgErr.ConstraintName, true
	}
	return "", "", false
}

func IsUniqueViolation(err error, constraint string) bool {
	code, name, ok := ConstraintViolation(err)
	return ok && code == "23505" && (constraint == "" || name == constraint)
}
