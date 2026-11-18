package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	SandboxBaseURL    = "https://sandbox-quickbooks.api.intuit.com"
	ProductionBaseURL = "https://quickbooks.api.intuit.com"
	minorVersion      = "75"
)

// Client is the live QuickBooks Online provider. It pages through the Query API, waits out
// throttling (HTTP 429, honouring Retry-After) and retries server errors with backoff before
// handing a TransientError to the job runner.
type Client struct {
	HTTP       *http.Client
	BaseURL    string
	MaxRetries int
	Sleep      func(ctx context.Context, d time.Duration) error
}

func NewClient(environment string) *Client {
	base := SandboxBaseURL
	if environment == "production" {
		base = ProductionBaseURL
	}
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, BaseURL: base, MaxRetries: 3, Sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) Mode() string { return ModeLive }

type fault struct {
	Fault struct {
		Error []struct {
			Message string `json:"Message"`
			Detail  string `json:"Detail"`
			Code    string `json:"code"`
		} `json:"Error"`
		Type string `json:"type"`
	} `json:"Fault"`
}

func backoff(attempt int) time.Duration {
	d := time.Duration(1<<attempt) * time.Second
	return d + time.Duration(rand.Int64N(int64(d/4)+1))
}

// query runs one page of a QuickBooks query and returns the raw entity objects.
func (c *Client) query(ctx context.Context, s Session, entity string, start, max int) ([]json.RawMessage, error) {
	q := fmt.Sprintf("SELECT * FROM %s STARTPOSITION %d MAXRESULTS %d", entity, start, max)
	endpoint := fmt.Sprintf("%s/v3/company/%s/query?query=%s&minorversion=%s", c.BaseURL, url.PathEscape(s.RealmID), url.QueryEscape(q), minorVersion)

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.AccessToken)
		req.Header.Set("Accept", "application/json")

		res, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < c.MaxRetries {
				if err := c.Sleep(ctx, backoff(attempt)); err != nil {
					return nil, err
				}
				continue
			}
			return nil, &TransientError{Err: fmt.Errorf("request to QuickBooks failed: %w", err)}
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		res.Body.Close()
		if readErr != nil {
			return nil, &TransientError{Err: readErr}
		}

		switch {
		case res.StatusCode == http.StatusOK:
			var envelope struct {
				QueryResponse map[string]json.RawMessage `json:"QueryResponse"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				return nil, fmt.Errorf("decode QuickBooks response: %w", err)
			}
			rawList, ok := envelope.QueryResponse[entity]
			if !ok {
				return nil, nil
			}
			var items []json.RawMessage
			if err := json.Unmarshal(rawList, &items); err != nil {
				return nil, fmt.Errorf("decode %s list: %w", entity, err)
			}
			return items, nil

		case res.StatusCode == http.StatusUnauthorized:
			return nil, ErrUnauthorized

		case res.StatusCode == http.StatusTooManyRequests:
			if attempt >= c.MaxRetries {
				return nil, ErrRateLimited
			}
			wait := backoff(attempt + 2)
			if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 && s <= 120 {
				wait = time.Duration(s) * time.Second
			}
			if err := c.Sleep(ctx, wait); err != nil {
				return nil, err
			}

		case res.StatusCode >= 500:
			if attempt >= c.MaxRetries {
				return nil, &TransientError{Err: fmt.Errorf("QuickBooks returned %d", res.StatusCode)}
			}
			if err := c.Sleep(ctx, backoff(attempt)); err != nil {
				return nil, err
			}

		default:
			var f fault
			_ = json.Unmarshal(body, &f)
			e := &APIError{Status: res.StatusCode, Message: http.StatusText(res.StatusCode)}
			if len(f.Fault.Error) > 0 {
				e.Code = f.Fault.Error[0].Code
				e.Message = f.Fault.Error[0].Message
				if d := f.Fault.Error[0].Detail; d != "" {
					e.Message += ": " + d
				}
			}
			return nil, e
		}
	}
}

func decodeAll[T any](items []json.RawMessage, base string, decode func(json.RawMessage, string) (T, error)) ([]T, error) {
	out := make([]T, 0, len(items))
	var errs []error
	for _, raw := range items {
		v, err := decode(raw, base)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func (c *Client) Customers(ctx context.Context, s Session, start, max int) ([]Customer, error) {
	items, err := c.query(ctx, s, "Customer", start, max)
	if err != nil {
		return nil, err
	}
	return decodeAll(items, s.BaseCurrency, decodeCustomer)
}

func (c *Client) Invoices(ctx context.Context, s Session, start, max int) ([]Invoice, error) {
	items, err := c.query(ctx, s, "Invoice", start, max)
	if err != nil {
		return nil, err
	}
	return decodeAll(items, s.BaseCurrency, decodeInvoice)
}

func (c *Client) Payments(ctx context.Context, s Session, start, max int) ([]Payment, error) {
	items, err := c.query(ctx, s, "Payment", start, max)
	if err != nil {
		return nil, err
	}
	return decodeAll(items, s.BaseCurrency, decodePayment)
}
