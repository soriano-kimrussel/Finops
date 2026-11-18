package reconcile

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var base = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func statuses(rs []Result) map[string]string {
	out := map[string]string{}
	for _, r := range rs {
		out[r.Key] = r.Status
	}
	return out
}

func TestCompareInvoices(t *testing.T) {
	a, b, c, z := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	extID := "QB-77"
	internal := []InternalInvoice{
		{ID: a, Number: "INV-1", Date: base, Total: d("100.00"), Currency: "USD"},
		{ID: b, Number: "INV-2", Date: base, Total: d("250.00"), Currency: "USD"},
		{ID: c, Number: "INV-3", Date: base, Total: d("80.00"), Currency: "USD"},
		{ID: z, Number: "QB-1050", Date: base, Total: d("40.00"), Currency: "USD", ExternalID: &extID},
		{ID: uuid.New(), Number: "INV-9", Date: base, Total: d("10.00"), Currency: "USD"},
	}
	external := []ExternalInvoice{
		{RecordID: uuid.New(), ExternalID: "e1", DocNumber: "INV-1", Date: base, Amount: d("100.00"), Currency: "USD"},
		{RecordID: uuid.New(), ExternalID: "e2", DocNumber: "INV-2", Date: base, Amount: d("275.00"), Currency: "USD"},
		{RecordID: uuid.New(), ExternalID: "e3", DocNumber: "INV-3", Date: base.AddDate(0, 0, 2), Amount: d("80"), Currency: "USD"},
		{RecordID: uuid.New(), ExternalID: "e4", DocNumber: "1043", Date: base, Amount: d("5"), Currency: "USD"},
		{RecordID: uuid.New(), ExternalID: extID, DocNumber: "something-else", Date: base, Amount: d("40.00"), Currency: "USD"},
	}
	rs := Compare(internal, nil, external, nil)
	got := statuses(rs)
	want := map[string]string{
		"invoice:ext:e1":    Matched,
		"invoice:ext:e2":    AmountMismatch,
		"invoice:ext:e3":    DateMismatch,
		"invoice:ext:e4":    MissingInternal,
		"invoice:ext:QB-77": Matched,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
	missing := 0
	for _, r := range rs {
		if r.Status == MissingExternal {
			missing++
			if r.Reference != "INV-9" {
				t.Errorf("unexpected missing_external %s", r.Reference)
			}
		}
		if r.Key == "invoice:ext:e2" && !r.Difference().Equal(d("25")) {
			t.Errorf("difference = %s", r.Difference())
		}
	}
	if missing != 1 {
		t.Errorf("expected 1 missing_external, got %d", missing)
	}
}

func TestNoExternalDataMeansNoMissingExternal(t *testing.T) {
	rs := Compare([]InternalInvoice{{ID: uuid.New(), Number: "INV-1", Date: base, Total: d("1"), Currency: "USD"}}, nil, nil, nil)
	if len(rs) != 0 {
		t.Fatalf("before any import nothing should be flagged, got %v", rs)
	}
}

func TestComparePayments(t *testing.T) {
	inv := uuid.New()
	p1, p2, p3 := uuid.New(), uuid.New(), uuid.New()
	internal := []InternalInvoice{{ID: inv, Number: "INV-1", Date: base, Total: d("300"), Currency: "USD"}}
	external := []ExternalInvoice{{RecordID: uuid.New(), ExternalID: "e1", DocNumber: "INV-1", Date: base, Amount: d("300"), Currency: "USD"}}
	intPays := []InternalPayment{
		{ID: p1, InvoiceID: inv, Amount: d("100"), Date: base.AddDate(0, 0, 3), Currency: "USD"},
		{ID: p2, InvoiceID: inv, Amount: d("150"), Date: base.AddDate(0, 0, 10), Currency: "USD"},
		{ID: p3, InvoiceID: inv, Amount: d("50"), Date: base.AddDate(0, 0, 20), Currency: "USD"},
	}
	extPays := []ExternalPayment{
		{RecordID: uuid.New(), ExternalID: "x1", InvoiceExternalID: "e1", Date: base.AddDate(0, 0, 3), Amount: d("100"), Currency: "USD"},
		{RecordID: uuid.New(), ExternalID: "x2", InvoiceExternalID: "e1", Date: base.AddDate(0, 0, 12), Amount: d("150"), Currency: "USD"},
		{RecordID: uuid.New(), ExternalID: "x4", InvoiceExternalID: "unknown", Date: base, Amount: d("9"), Currency: "USD"},
	}
	got := statuses(Compare(internal, intPays, external, extPays))
	if got["payment:ext:x1"] != Matched || got["payment:ext:x2"] != DateMismatch || got["payment:ext:x4"] != MissingInternal {
		t.Fatalf("payments: %v", got)
	}
	if got["payment:int:"+p3.String()] != MissingExternal {
		t.Fatalf("unmatched internal payment should be missing_external: %v", got)
	}
}

func TestFingerprintChangesWithContent(t *testing.T) {
	a := Result{Status: AmountMismatch, Key: "k", ExternalAmount: ptr(d("10")), InternalAmount: ptr(d("9"))}
	b := a
	b.ExternalAmount = ptr(d("11"))
	if string(a.Fingerprint()) == string(b.Fingerprint()) {
		t.Fatal("different amounts must produce different fingerprints")
	}
	c := a
	if string(a.Fingerprint()) != string(c.Fingerprint()) {
		t.Fatal("identical results must have identical fingerprints")
	}
}
