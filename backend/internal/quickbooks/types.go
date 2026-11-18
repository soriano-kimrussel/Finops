// Package quickbooks talks to QuickBooks Online (Intuit's accounting API) and also provides a
// clearly labelled simulated provider for development without Intuit credentials.
package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	ModeLive      = "live"
	ModeSimulated = "simulated"
)

type Customer struct {
	ID          string
	DisplayName string
	Email       string
	Phone       string
	Currency    string
	Active      bool
	Line1       string
	City        string
	Region      string
	PostalCode  string
	Country     string
	UpdatedAt   *time.Time
	Raw         json.RawMessage
}

type InvoiceLine struct {
	Description string
	Amount      decimal.Decimal
	Quantity    *decimal.Decimal
	UnitPrice   *decimal.Decimal
}

type Invoice struct {
	ID           string
	DocNumber    string
	CustomerID   string
	CustomerName string
	TxnDate      time.Time
	DueDate      *time.Time
	Total        decimal.Decimal
	Balance      decimal.Decimal
	TotalTax     decimal.Decimal
	Currency     string
	Lines        []InvoiceLine
	UpdatedAt    *time.Time
	Raw          json.RawMessage
}

type PaymentApplication struct {
	InvoiceID string
	Amount    decimal.Decimal
}

type Payment struct {
	ID         string
	CustomerID string
	TxnDate    time.Time
	Total      decimal.Decimal
	Currency   string
	Applied    []PaymentApplication
	UpdatedAt  *time.Time
	Raw        json.RawMessage
}

// Session carries what a provider needs for one organization's requests.
type Session struct {
	OrgID        uuid.UUID
	RealmID      string
	AccessToken  string
	BaseCurrency string
}

type Provider interface {
	Mode() string
	Customers(ctx context.Context, s Session, start, max int) ([]Customer, error)
	Invoices(ctx context.Context, s Session, start, max int) ([]Invoice, error)
	Payments(ctx context.Context, s Session, start, max int) ([]Payment, error)
}

var (
	ErrUnauthorized = errors.New("quickbooks rejected the access token")
	ErrRateLimited  = errors.New("quickbooks rate limit exceeded")
)

// TransientError marks failures worth retrying later (network, 5xx, throttling).
type TransientError struct{ Err error }

func (e *TransientError) Error() string { return "transient: " + e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// APIError is a non-retryable error returned by QuickBooks, such as a validation fault.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("quickbooks API error %d %s: %s", e.Status, e.Code, e.Message)
}

func IsTransient(err error) bool {
	var t *TransientError
	return errors.As(err, &t) || errors.Is(err, ErrRateLimited) || errors.Is(err, context.DeadlineExceeded)
}
