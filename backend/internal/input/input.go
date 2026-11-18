// Package input holds small parsing helpers shared by the HTTP handlers and services.
package input

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"finopshq/internal/apperr"
)

const DateLayout = "2006-01-02"

// ParseDate reads a calendar date (YYYY-MM-DD) as midnight UTC.
func ParseDate(s string) (time.Time, error) {
	return time.Parse(DateLayout, strings.TrimSpace(s))
}

func FormatDate(t time.Time) string { return t.Format(DateLayout) }

func FormatDatePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := FormatDate(*t)
	return &s
}

// Date validates a required date field.
func Date(v *apperr.Validation, field, s string) time.Time {
	if strings.TrimSpace(s) == "" {
		v.Add(field, "is required")
		return time.Time{}
	}
	t, err := ParseDate(s)
	if err != nil {
		v.Add(field, "must be a date in YYYY-MM-DD format")
		return time.Time{}
	}
	if t.Year() < 1900 || t.Year() > 2200 {
		v.Add(field, "is out of range")
	}
	return t
}

// OptionalDate validates an optional date field; nil or empty means no value.
func OptionalDate(v *apperr.Validation, field string, s *string) *time.Time {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	t := Date(v, field, *s)
	if v.Has(field) {
		return nil
	}
	return &t
}

// Text trims a required string and checks its length in characters.
func Text(v *apperr.Validation, field, s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		v.Add(field, "is required")
	} else if utf8.RuneCountInString(s) > max {
		v.Add(field, "is too long (max "+strconv.Itoa(max)+" characters)")
	}
	return s
}

// OptionalText trims an optional string; empty becomes nil.
func OptionalText(v *apperr.Validation, field string, s *string, max int) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	if utf8.RuneCountInString(t) > max {
		v.Add(field, "is too long (max "+strconv.Itoa(max)+" characters)")
	}
	return &t
}

// OptionalUUID parses an optional id from a request body.
func OptionalUUID(v *apperr.Validation, field string, s *string) *uuid.UUID {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	id, err := uuid.Parse(strings.TrimSpace(*s))
	if err != nil {
		v.Add(field, "is not a valid id")
		return nil
	}
	return &id
}

func RequiredUUID(v *apperr.Validation, field, s string) uuid.UUID {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		v.Add(field, "is required")
		return uuid.Nil
	}
	return id
}

// Pattern turns a search query into an ILIKE pattern, escaping wildcards in the user input.
func Pattern(q string) *string {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	p := "%" + r.Replace(q) + "%"
	return &p
}

type Page struct {
	Limit  int32
	Offset int32
}

func PageFrom(r *http.Request) Page {
	p := Page{Limit: 50}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		p.Limit = int32(min(n, 200))
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
		p.Offset = int32(min(n, 1_000_000))
	}
	return p
}

// Query helpers return a 400 for malformed filters instead of silently ignoring them.

func QueryString(r *http.Request, name string) *string {
	s := strings.TrimSpace(r.URL.Query().Get(name))
	if s == "" {
		return nil
	}
	return &s
}

func QueryEnum(r *http.Request, name string, allowed ...string) (*string, error) {
	s := QueryString(r, name)
	if s == nil {
		return nil, nil
	}
	for _, a := range allowed {
		if *s == a {
			return s, nil
		}
	}
	return nil, apperr.BadRequest("invalid " + name + " filter")
}

func QueryUUID(r *http.Request, name string) (*uuid.UUID, error) {
	s := QueryString(r, name)
	if s == nil {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, apperr.BadRequest("invalid " + name + " filter")
	}
	return &id, nil
}

func QueryDate(r *http.Request, name string) (*time.Time, error) {
	s := QueryString(r, name)
	if s == nil {
		return nil, nil
	}
	t, err := ParseDate(*s)
	if err != nil {
		return nil, apperr.BadRequest(name + " must be a date in YYYY-MM-DD format")
	}
	return &t, nil
}

// DateRange reads from/to and rejects inverted ranges.
func DateRange(r *http.Request) (from, to *time.Time, err error) {
	if from, err = QueryDate(r, "from"); err != nil {
		return nil, nil, err
	}
	if to, err = QueryDate(r, "to"); err != nil {
		return nil, nil, err
	}
	if from != nil && to != nil && to.Before(*from) {
		return nil, nil, apperr.BadRequest("to must not be before from")
	}
	return from, to, nil
}
