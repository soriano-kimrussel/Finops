package quickbooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type fakeResponse struct {
	status  int
	body    string
	headers map[string]string
}

type fakeTransport struct {
	responses []fakeResponse
	requests  []*http.Request
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, req)
	if len(f.responses) == 0 {
		return nil, errors.New("no more fake responses")
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	h := http.Header{}
	for k, v := range r.headers {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: r.status, Header: h, Body: io.NopCloser(strings.NewReader(r.body)), Request: req}, nil
}

func newTestClient(responses ...fakeResponse) (*Client, *fakeTransport, *[]time.Duration) {
	ft := &fakeTransport{responses: responses}
	var slept []time.Duration
	c := &Client{HTTP: &http.Client{Transport: ft}, BaseURL: "https://qbo.test", MaxRetries: 3,
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}
	return c, ft, &slept
}

var sess = Session{OrgID: uuid.New(), RealmID: "9130 357", AccessToken: "access-123", BaseCurrency: "USD"}

const invoicePage = `{"QueryResponse":{"Invoice":[
 {"Id":"145","DocNumber":"1037","TxnDate":"2026-10-02","DueDate":"2026-11-01","TotalAmt":1234.56,"Balance":234.56,
  "CurrencyRef":{"value":"USD"},"CustomerRef":{"value":"58","name":"Halvorsen Freight Co."},
  "TxnTaxDetail":{"TotalTax":76.16},
  "Line":[{"Amount":1158.40,"Description":"Advisory","DetailType":"SalesItemLineDetail","SalesItemLineDetail":{"Qty":6.4,"UnitPrice":181}},
          {"Amount":1158.40,"DetailType":"SubTotalLineDetail"}],
  "MetaData":{"LastUpdatedTime":"2026-10-03T10:15:00-07:00"}},
 {"Id":"146","DocNumber":"1038","TxnDate":"not-a-date","TotalAmt":10,"CustomerRef":{"value":"58"}}
],"startPosition":1,"maxResults":2},"time":"2026-11-18T10:00:00.000-08:00"}`

func TestQueryDecodesInvoicesExactly(t *testing.T) {
	c, ft, _ := newTestClient(fakeResponse{status: 200, body: invoicePage})
	invs, err := c.Invoices(context.Background(), sess, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(invs) != 1 {
		t.Fatalf("the malformed record should be skipped, got %d", len(invs))
	}
	inv := invs[0]
	if !inv.Total.Equal(decimal.RequireFromString("1234.56")) || !inv.Balance.Equal(decimal.RequireFromString("234.56")) || !inv.TotalTax.Equal(decimal.RequireFromString("76.16")) {
		t.Fatalf("amounts decoded inexactly: %s %s %s", inv.Total, inv.Balance, inv.TotalTax)
	}
	if inv.DocNumber != "1037" || inv.CustomerID != "58" || inv.TxnDate.Format("2006-01-02") != "2026-10-02" || inv.DueDate == nil || len(inv.Lines) != 1 {
		t.Fatalf("unexpected invoice %+v", inv)
	}
	if !inv.Lines[0].Quantity.Equal(decimal.RequireFromString("6.4")) {
		t.Fatalf("quantity %s", inv.Lines[0].Quantity)
	}

	req := ft.requests[0]
	if req.Header.Get("Authorization") != "Bearer access-123" || !strings.Contains(req.URL.String(), "/v3/company/9130%20357/query") {
		t.Fatalf("unexpected request %s %v", req.URL, req.Header)
	}
	if q := req.URL.Query().Get("query"); q != "SELECT * FROM Invoice STARTPOSITION 1 MAXRESULTS 100" {
		t.Fatalf("query %q", q)
	}
}

func TestRateLimitHonoursRetryAfter(t *testing.T) {
	c, ft, slept := newTestClient(
		fakeResponse{status: 429, headers: map[string]string{"Retry-After": "7"}},
		fakeResponse{status: 200, body: `{"QueryResponse":{}}`},
	)
	got, err := c.Customers(context.Background(), sess, 1, 50)
	if err != nil || len(got) != 0 {
		t.Fatalf("expected an empty page after waiting, got %v %v", got, err)
	}
	if len(ft.requests) != 2 || len(*slept) != 1 || (*slept)[0] != 7*time.Second {
		t.Fatalf("requests %d, slept %v", len(ft.requests), *slept)
	}
}

func TestPersistentThrottlingBecomesRetryable(t *testing.T) {
	var rs []fakeResponse
	for i := 0; i < 4; i++ {
		rs = append(rs, fakeResponse{status: 429})
	}
	c, _, _ := newTestClient(rs...)
	_, err := c.Payments(context.Background(), sess, 1, 50)
	if !errors.Is(err, ErrRateLimited) || !IsTransient(err) {
		t.Fatalf("expected retryable rate-limit error, got %v", err)
	}
}

func TestServerErrorsRetryThenFailTransiently(t *testing.T) {
	c, ft, _ := newTestClient(fakeResponse{status: 503}, fakeResponse{status: 502}, fakeResponse{status: 500}, fakeResponse{status: 500})
	_, err := c.Invoices(context.Background(), sess, 1, 50)
	if !IsTransient(err) || len(ft.requests) != 4 {
		t.Fatalf("expected transient error after 4 attempts, got %v after %d", err, len(ft.requests))
	}
}

func TestUnauthorizedAndFaults(t *testing.T) {
	c, _, _ := newTestClient(fakeResponse{status: 401, body: `{"fault":{}}`})
	if _, err := c.Invoices(context.Background(), sess, 1, 50); !errors.Is(err, ErrUnauthorized) || IsTransient(err) {
		t.Fatalf("expected non-retryable unauthorized, got %v", err)
	}
	c, _, _ = newTestClient(fakeResponse{status: 400, body: `{"Fault":{"Error":[{"Message":"Error parsing query","Detail":"QueryParserError","code":"4000"}],"type":"ValidationFault"}}`})
	_, err := c.Invoices(context.Background(), sess, 1, 50)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "4000" || IsTransient(err) {
		t.Fatalf("expected APIError 4000, got %v", err)
	}
}

func TestValidation(t *testing.T) {
	good := Invoice{ID: "1", CustomerID: "2", Total: decimal.NewFromInt(10), Balance: decimal.NewFromInt(5), Currency: "USD"}
	if err := ValidateInvoice(good); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.Balance = decimal.NewFromInt(11)
	if ValidateInvoice(bad) == nil {
		t.Error("balance above total must be rejected")
	}
	if ValidatePayment(Payment{ID: "p", Total: decimal.NewFromInt(5), Currency: "USD", Applied: []PaymentApplication{{InvoiceID: "1", Amount: decimal.NewFromInt(6)}}}) == nil {
		t.Error("over-applied payment must be rejected")
	}
	if ValidateCustomer(Customer{ID: "1"}) == nil {
		t.Error("nameless customer must be rejected")
	}
}

func TestWebhookSignature(t *testing.T) {
	body := []byte(`{"eventNotifications":[{"realmId":"123"}]}`)
	mac := hmac.New(sha256.New, []byte("verifier"))
	mac.Write(body)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !VerifyWebhook(body, sig, "verifier") {
		t.Fatal("valid signature rejected")
	}
	if VerifyWebhook(append(body, ' '), sig, "verifier") || VerifyWebhook(body, sig, "other") || VerifyWebhook(body, "", "verifier") || VerifyWebhook(body, sig, "") {
		t.Fatal("invalid signatures must be rejected")
	}
}

func TestSimulatedProviderIsLabelledAndDeterministic(t *testing.T) {
	today := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	custID := uuid.New()
	snap := Snapshot{Today: today, Customers: []SnapCustomer{{ID: custID, Name: "Acme", Currency: "USD"}}}
	for i := 0; i < 12; i++ {
		snap.Invoices = append(snap.Invoices, SnapInvoice{ID: uuid.New(), Number: "INV-" + string(rune('A'+i)), CustomerID: custID, CustomerName: "Acme",
			IssueDate: today.AddDate(0, 0, -30), DueDate: today, Total: decimal.NewFromInt(100), Currency: "USD"})
	}
	sim := &Simulated{Snapshot: func(context.Context, uuid.UUID) (Snapshot, error) { return snap, nil }}
	invs, err := sim.Invoices(context.Background(), sess, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(invs) != 13 {
		t.Fatalf("12 internal - 1 missing + 2 QuickBooks-only = 13, got %d", len(invs))
	}
	if !invs[4].Total.Equal(decimal.NewFromInt(125)) || !invs[3].TxnDate.Equal(today.AddDate(0, 0, -28)) {
		t.Fatal("deliberate discrepancies missing")
	}
	for _, inv := range invs {
		if !strings.Contains(string(inv.Raw), `"simulated":true`) {
			t.Fatalf("simulated record %s is not labelled", inv.ID)
		}
	}
	second, _ := sim.Invoices(context.Background(), sess, 1, 5)
	if len(second) != 5 || second[0].ID != invs[0].ID {
		t.Fatal("paging must be stable")
	}
	if p, _ := sim.Invoices(context.Background(), sess, 14, 5); len(p) != 0 {
		t.Fatal("past the end must return an empty page")
	}
}
