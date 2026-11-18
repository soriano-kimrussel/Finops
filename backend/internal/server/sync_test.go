package server_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	gosync "sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"finopshq/internal/apitest"
	"finopshq/internal/qbsync"
	"finopshq/internal/quickbooks"
	"finopshq/internal/secrets"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

// booksWithInvoices creates n sent invoices of 100.00 each and pays the first two in full.
func booksWithInvoices(t *testing.T, n int) *books {
	b := newBooks(t, "USD")
	for i := 0; i < n; i++ {
		inv := b.sentInvoice(t, "100.00").Invoice
		if i < 2 {
			b.owner.Must(http.StatusCreated, b.pay(b.owner, inv.ID, "100.00", "sync-pay-"+inv.Number))
		}
	}
	return b
}

func (b *books) connect(t *testing.T) {
	t.Helper()
	res := b.owner.Must(http.StatusOK, b.owner.Post(b.url("/integrations/quickbooks/connect"), nil)).Map(t)
	if res["connection"].(map[string]any)["mode"] != "simulated" {
		t.Fatalf("expected simulated connection, got %v", res)
	}
}

type jobBody struct {
	Job struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		Attempts  int    `json:"attempts"`
		LastError string `json:"last_error"`
		Stats     struct {
			Customers *qbsync.EntityStats `json:"customers"`
			Invoices  *qbsync.EntityStats `json:"invoices"`
			Payments  *qbsync.EntityStats `json:"payments"`
			Pages     int                 `json:"pages"`
		} `json:"stats"`
	} `json:"job"`
	Existing bool `json:"existing"`
	Attempts []struct {
		Outcome string `json:"outcome"`
	} `json:"attempts"`
}

func (b *books) job(t *testing.T, id string) jobBody {
	var j jobBody
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/sync-jobs/"+id))).JSON(t, &j)
	return j
}

func countRows(t *testing.T, b *books, sql string) int {
	t.Helper()
	var n int
	if err := b.env.DB.Owner.QueryRow(context.Background(), sql, b.org).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSimulatedSyncImportsAndReconciles(t *testing.T) {
	b := booksWithInvoices(t, 12)
	if r := b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "full"}); r.ErrorCode(t) != "not_connected" {
		t.Fatalf("sync before connecting must fail: %s", r.Body)
	}
	b.connect(t)

	var first jobBody
	b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "full"})).JSON(t, &first)
	var dup jobBody
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "full"})).JSON(t, &dup)
	if !dup.Existing || dup.Job.ID != first.Job.ID {
		t.Fatal("a second request while a job is queued must return the same job")
	}

	if n := b.env.RunJobs(); n != 1 {
		t.Fatalf("expected one job processed, got %d", n)
	}
	j := b.job(t, first.Job.ID)
	if j.Job.Status != "succeeded" || len(j.Attempts) != 1 || j.Attempts[0].Outcome != "succeeded" {
		t.Fatalf("job %+v", j)
	}
	// 12 internal invoices: one omitted by the simulator, two QuickBooks-only => 13 fetched, across pages of 7.
	// Pages of 7: customers 1 (2 records), invoices 2 (13), payments 1 (3).
	if j.Job.Stats.Invoices.Fetched != 13 || j.Job.Stats.Invoices.Created != 13 || j.Job.Stats.Pages != 4 {
		t.Fatalf("invoice stats %+v pages %d", j.Job.Stats.Invoices, j.Job.Stats.Pages)
	}
	if j.Job.Stats.Customers.Linked != 1 || j.Job.Stats.Customers.Imported != 1 {
		t.Fatalf("customer stats %+v", j.Job.Stats.Customers)
	}

	var rec struct {
		Data []struct {
			ID           string `json:"id"`
			MatchStatus  string `json:"match_status"`
			ReviewStatus string `json:"review_status"`
			EntityType   string `json:"entity_type"`
			Reference    string `json:"reference"`
			Difference   string `json:"difference"`
		} `json:"data"`
		Summary map[string]map[string]int `json:"summary"`
	}
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reconciliation?limit=200"))).JSON(t, &rec)
	s := rec.Summary
	if s["amount_mismatch"]["open"] != 1 || s["date_mismatch"]["open"] != 1 || s["missing_external"]["open"] < 1 || s["missing_internal"]["open"] < 2 {
		t.Fatalf("reconciliation summary %v", s)
	}
	if s["matched"]["not_required"] < 8 {
		t.Fatalf("expected most invoices to match, summary %v", s)
	}
	for _, r := range rec.Data {
		if r.MatchStatus == "amount_mismatch" && r.EntityType == "invoice" && r.Difference != "25.00" {
			t.Fatalf("amount mismatch difference %s", r.Difference)
		}
	}

	// A second sync with no changes is a no-op: nothing duplicated, no new review items.
	openBefore := countRows(t, b, "SELECT count(*) FROM reconciliation_records WHERE organization_id = $1 AND review_status = 'open'")
	b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "full"}))
	b.env.RunJobs()
	if n := countRows(t, b, "SELECT count(*) FROM external_records WHERE organization_id = $1 AND entity_type = 'invoice'"); n != 13 {
		t.Fatalf("re-sync must not duplicate records, got %d", n)
	}
	if n := countRows(t, b, "SELECT count(*) FROM customers WHERE organization_id = $1 AND source = 'quickbooks'"); n != 1 {
		t.Fatalf("re-sync must not duplicate customers, got %d", n)
	}
	if n := countRows(t, b, "SELECT count(*) FROM reconciliation_records WHERE organization_id = $1 AND review_status = 'open'"); n != openBefore {
		t.Fatalf("re-sync must not reopen items: %d -> %d", openBefore, n)
	}
}

func TestReconciliationDecisions(t *testing.T) {
	b := booksWithInvoices(t, 12)
	b.connect(t)
	b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "full"}))
	b.env.RunJobs()

	find := func(status, entity string) string {
		var res struct {
			Data []struct {
				ID          string `json:"id"`
				EntityType  string `json:"entity_type"`
				Reference   string `json:"reference"`
				ReviewState string `json:"review_status"`
			} `json:"data"`
		}
		b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reconciliation?review_status=open&match_status="+status+"&entity_type="+entity))).JSON(t, &res)
		if len(res.Data) == 0 {
			t.Fatalf("no open %s %s record", entity, status)
		}
		return res.Data[0].ID
	}

	mismatch := find("amount_mismatch", "invoice")
	if r := b.owner.Post(b.url("/reconciliation/"+mismatch+"/review"), map[string]string{"decision": "accepted"}); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("a decision without a note must be rejected, got %d", r.Status)
	}
	if r := b.owner.Post(b.url("/reconciliation/"+mismatch+"/review"), map[string]string{"decision": "imported", "note": "x"}); r.ErrorCode(t) != "invalid_decision" {
		t.Fatalf("import is not valid for an amount mismatch: %s", r.Body)
	}
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/reconciliation/"+mismatch+"/review"), map[string]string{"decision": "accepted", "note": "Late fee added in QuickBooks only"}))
	if r := b.owner.Post(b.url("/reconciliation/"+mismatch+"/review"), map[string]string{"decision": "ignored", "note": "changed my mind"}); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("a recorded decision is final, got %d", r.Status)
	}

	missing := find("missing_internal", "invoice")
	var decided struct {
		Record struct {
			Decision  string `json:"decision"`
			InvoiceID string `json:"invoice_id"`
		} `json:"record"`
	}
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/reconciliation/"+missing+"/review"), map[string]string{"decision": "imported"})).JSON(t, &decided)
	if decided.Record.InvoiceID == "" {
		t.Fatal("import must link the created invoice")
	}
	var inv invoiceBody
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/invoices/"+decided.Record.InvoiceID))).JSON(t, &inv)
	if inv.Invoice.Status != "sent" || (inv.Invoice.Total != "1850.00" && inv.Invoice.Total != "640.00") {
		t.Fatalf("imported invoice %+v", inv.Invoice)
	}

	// After the import, a new reconciliation run sees that invoice as matched through its link.
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/reconciliation/run"), nil))
	var detail struct {
		History []struct {
			ReviewStatus string `json:"review_status"`
			Decision     string `json:"decision"`
		} `json:"history"`
	}
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reconciliation/"+missing))).JSON(t, &detail)
	if len(detail.History) != 2 || detail.History[0].Decision != "imported" || detail.History[0].ReviewStatus != "superseded" || detail.History[1].ReviewStatus != "not_required" {
		t.Fatalf("history should show the imported decision superseded by a match: %+v", detail.History)
	}

	if n := countRows(t, b, "SELECT count(*) FROM audit_logs WHERE organization_id = $1 AND action = 'reconciliation.decided'"); n != 2 {
		t.Fatalf("expected 2 audited decisions, got %d", n)
	}
	ctx := context.Background()
	err := tenant.WithTx(ctx, b.env.DB.App, tenant.Scope{OrgID: uuid.MustParse(b.org)}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE reconciliation_records SET decision_note = 'rewritten' WHERE decision = 'accepted'")
		return err
	})
	if err == nil {
		t.Fatal("recorded decisions must not be editable even with direct SQL")
	}
}

func TestTransientFailureRetriesWithBackoffThenSucceeds(t *testing.T) {
	b := booksWithInvoices(t, 3)
	b.connect(t)
	sim := b.env.Worker.Simulated.(*quickbooks.Simulated)
	fails := 2
	var mu gosync.Mutex
	sim.FailNext = func(entity string, _ int) error {
		mu.Lock()
		defer mu.Unlock()
		if entity == "Invoice" && fails > 0 {
			fails--
			return &quickbooks.TransientError{Err: errors.New("connection reset by peer")}
		}
		return nil
	}
	var j jobBody
	b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "invoices"})).JSON(t, &j)

	b.env.RunJobs()
	got := b.job(t, j.Job.ID)
	if got.Job.Status != "retrying" || got.Job.Attempts != 1 || got.Job.LastError == "" {
		t.Fatalf("after a transient failure the job should wait to retry: %+v", got.Job)
	}
	if b.env.RunJobs() != 0 {
		t.Fatal("a retrying job must not run before its backoff elapses")
	}
	makeDue := func() {
		if _, err := b.env.DB.Owner.Exec(context.Background(), "UPDATE sync_jobs SET run_at = now() WHERE id = $1", j.Job.ID); err != nil {
			t.Fatal(err)
		}
	}
	makeDue()
	b.env.RunJobs()
	makeDue()
	b.env.RunJobs()
	got = b.job(t, j.Job.ID)
	if got.Job.Status != "succeeded" || got.Job.Attempts != 3 || len(got.Attempts) != 3 {
		t.Fatalf("expected success on the third attempt: %+v %+v", got.Job, got.Attempts)
	}
	if got.Attempts[0].Outcome != "retrying" || got.Attempts[2].Outcome != "succeeded" {
		t.Fatalf("attempt history %+v", got.Attempts)
	}
}

func TestPermanentFailureAndManualRetry(t *testing.T) {
	b := booksWithInvoices(t, 2)
	b.connect(t)
	sim := b.env.Worker.Simulated.(*quickbooks.Simulated)
	broken := true
	sim.FailNext = func(string, int) error {
		if broken {
			return &quickbooks.APIError{Status: 400, Code: "4000", Message: "Error parsing query"}
		}
		return nil
	}
	var j jobBody
	b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "full"})).JSON(t, &j)
	b.env.RunJobs()
	if got := b.job(t, j.Job.ID); got.Job.Status != "failed" || got.Job.Attempts != 1 {
		t.Fatalf("a non-retryable error must fail immediately: %+v", got.Job)
	}
	if n := countRows(t, b, "SELECT count(*) FROM audit_logs WHERE organization_id = $1 AND action = 'sync.failed'"); n != 1 {
		t.Fatalf("failure must be audited, got %d", n)
	}

	broken = false
	var retry jobBody
	b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs/"+j.Job.ID+"/retry"), nil)).JSON(t, &retry)
	b.env.RunJobs()
	if got := b.job(t, retry.Job.ID); got.Job.Status != "succeeded" {
		t.Fatalf("manual retry should succeed: %+v", got.Job)
	}
	if r := b.owner.Post(b.url("/sync-jobs/"+retry.Job.ID+"/retry"), nil); r.ErrorCode(t) != "not_failed" {
		t.Fatalf("only failed jobs can be retried: %s", r.Body)
	}
}

func TestConcurrentWorkersProcessEachJobOnce(t *testing.T) {
	env := apitest.New(t)
	var orgs []*books
	for i := 0; i < 4; i++ {
		c := env.Client()
		c.Register("Owner")
		org := c.CreateOrg("Org", "USD")
		b := &books{env: env, owner: c, org: org}
		b.connect(t)
		b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "customers"}))
		orgs = append(orgs, b)
	}
	var wg gosync.WaitGroup
	for i := 0; i < 6; i++ {
		w := qbsync.Worker{Pool: env.DB.App, Simulated: env.Worker.Simulated, AfterSync: env.Worker.AfterSync, PageSize: 7,
			Backoff: env.Worker.Backoff, ID: "worker-" + string(rune('a'+i))}
		wg.Add(1)
		go func(w *qbsync.Worker) {
			defer wg.Done()
			for {
				worked, err := w.RunOnce(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if !worked {
					return
				}
			}
		}(&w)
	}
	wg.Wait()
	var succeeded, attempts int
	if err := env.DB.Owner.QueryRow(context.Background(), "SELECT count(*) FILTER (WHERE status = 'succeeded'), coalesce(sum(attempts), 0) FROM sync_jobs").Scan(&succeeded, &attempts); err != nil {
		t.Fatal(err)
	}
	if succeeded != 4 || attempts != 4 {
		t.Fatalf("each job must be claimed exactly once: succeeded=%d attempts=%d", succeeded, attempts)
	}
}

func TestExpiredLeaseIsReclaimedAndStaleWorkerCannotFinish(t *testing.T) {
	b := booksWithInvoices(t, 1)
	b.connect(t)
	var j jobBody
	b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "customers"})).JSON(t, &j)
	ctx := context.Background()

	var id uuid.UUID
	var org uuid.UUID
	var attempt, max int32
	if err := b.env.DB.App.QueryRow(ctx, "SELECT * FROM claim_sync_job('crashed-worker', 60)").Scan(&id, &org, &attempt, &max); err != nil {
		t.Fatal(err)
	}
	if b.env.RunJobs() != 0 {
		t.Fatal("a job under a live lease must not be claimed by another worker")
	}
	if _, err := b.env.DB.Owner.Exec(ctx, "UPDATE sync_jobs SET locked_until = now() - interval '1 second' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	if b.env.RunJobs() != 1 {
		t.Fatal("an expired lease must be reclaimed")
	}
	got := b.job(t, j.Job.ID)
	if got.Job.Status != "succeeded" || got.Job.Attempts != 2 {
		t.Fatalf("reclaimed job %+v", got.Job)
	}
	var n int64
	err := tenant.WithTx(ctx, b.env.DB.App, tenant.Scope{OrgID: org}, func(tx pgx.Tx) error {
		var err error
		stale := "crashed-worker"
		n, err = store.New(tx).CompleteSyncJob(ctx, store.CompleteSyncJobParams{ID: id, LockedBy: &stale, Stats: []byte(`{}`)})
		return err
	})
	if err != nil || n != 0 {
		t.Fatalf("the original worker must not be able to complete a reclaimed job (rows=%d err=%v)", n, err)
	}
}

type fakeRefresher struct {
	calls int
	err   error
}

func (f *fakeRefresher) Refresh(_ context.Context, rt string) (quickbooks.Token, error) {
	f.calls++
	if f.err != nil {
		return quickbooks.Token{}, f.err
	}
	return quickbooks.Token{AccessToken: "access-" + rt + "-new", RefreshToken: "rotated-" + rt, AccessExpiry: time.Now().Add(time.Hour), RefreshExpiry: time.Now().Add(100 * 24 * time.Hour)}, nil
}

type fakeLive struct {
	tokens []string
	reject string
}

func (f *fakeLive) Mode() string { return quickbooks.ModeLive }
func (f *fakeLive) Customers(_ context.Context, s quickbooks.Session, _, _ int) ([]quickbooks.Customer, error) {
	f.tokens = append(f.tokens, s.AccessToken)
	if s.AccessToken == f.reject {
		return nil, quickbooks.ErrUnauthorized
	}
	return []quickbooks.Customer{{ID: "58", DisplayName: "Live Customer LLC", Currency: "USD", Active: true, Raw: []byte(`{"Id":"58"}`)}}, nil
}
func (f *fakeLive) Invoices(context.Context, quickbooks.Session, int, int) ([]quickbooks.Invoice, error) {
	return nil, nil
}
func (f *fakeLive) Payments(context.Context, quickbooks.Session, int, int) ([]quickbooks.Payment, error) {
	return nil, nil
}

// insertLiveConnection stores a live connection with encrypted tokens, as the OAuth callback would.
func insertLiveConnection(t *testing.T, b *books, box *secrets.Box, access string, expires time.Time) uuid.UUID {
	org := uuid.MustParse(b.org)
	ctx := context.Background()
	ae, _ := box.Seal([]byte(access), org[:])
	re, _ := box.Seal([]byte("refresh-1"), org[:])
	realm := "4620816365" + b.org[:4]
	var id uuid.UUID
	err := tenant.WithTx(ctx, b.env.DB.App, tenant.Scope{OrgID: org}, func(tx pgx.Tx) error {
		c, err := store.New(tx).UpsertConnection(ctx, store.UpsertConnectionParams{OrganizationID: org, Mode: "live", RealmID: &realm,
			AccessTokenEnc: ae, RefreshTokenEnc: re, AccessTokenExpiresAt: &expires})
		id = c.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLiveTokenRefreshAndRotation(t *testing.T) {
	b := newBooks(t, "USD")
	box, _ := secrets.New(b.env.Config.TokenEncryptionKey)
	connID := insertLiveConnection(t, b, box, "expired-access", time.Now().Add(-time.Minute))
	ref := &fakeRefresher{}
	live := &fakeLive{}
	b.env.Worker.Live = live
	b.env.Worker.Tokens = &qbsync.TokenManager{Pool: b.env.DB.App, Box: box, Refresher: ref}

	b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "customers"}))
	b.env.RunJobs()
	if ref.calls != 1 || len(live.tokens) == 0 || live.tokens[0] != "access-refresh-1-new" {
		t.Fatalf("expected one refresh before calling QuickBooks: calls=%d tokens=%v", ref.calls, live.tokens)
	}
	var enc []byte
	if err := b.env.DB.Owner.QueryRow(context.Background(), "SELECT refresh_token_enc FROM sync_connections WHERE id = $1", connID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	org := uuid.MustParse(b.org)
	rt, err := box.Open(enc, org[:])
	if err != nil || string(rt) != "rotated-refresh-1" {
		t.Fatalf("the rotated refresh token must be stored encrypted: %q %v", rt, err)
	}
	if bytes.Contains(enc, []byte("rotated")) {
		t.Fatal("tokens must not be stored in plain text")
	}
	if n := countRows(t, b, "SELECT count(*) FROM customers WHERE organization_id = $1 AND source = 'quickbooks'"); n != 1 {
		t.Fatalf("live customer should be imported, got %d", n)
	}
}

func TestRevokedRefreshTokenMarksConnectionForReauth(t *testing.T) {
	b := newBooks(t, "USD")
	box, _ := secrets.New(b.env.Config.TokenEncryptionKey)
	insertLiveConnection(t, b, box, "stale", time.Now().Add(time.Hour))
	ref := &fakeRefresher{err: quickbooks.ErrUnauthorized}
	b.env.Worker.Live = &fakeLive{reject: "stale"}
	b.env.Worker.Tokens = &qbsync.TokenManager{Pool: b.env.DB.App, Box: box, Refresher: ref}

	var j jobBody
	b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "customers"})).JSON(t, &j)
	b.env.RunJobs()
	if got := b.job(t, j.Job.ID); got.Job.Status != "failed" {
		t.Fatalf("401 after a failed refresh must fail the job: %+v", got.Job)
	}
	var st struct {
		Connection struct{ Status string } `json:"connection"`
	}
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/integrations/quickbooks"))).JSON(t, &st)
	if st.Connection.Status != "needs_reauth" || ref.calls != 1 {
		t.Fatalf("connection should need re-authorization: %+v (refresh calls %d)", st.Connection, ref.calls)
	}
}

func TestWebhookQueuesSyncForTheRightOrganization(t *testing.T) {
	env := apitest.NewWithConfig(t, func(c *apitest.ConfigT) { c.QuickBooks.WebhookVerifierToken = "verifier-secret" })
	c := env.Client()
	c.Register("Owner")
	org := c.CreateOrg("Hooked", "USD")
	b := &books{env: env, owner: c, org: org}
	box, _ := secrets.New(env.Config.TokenEncryptionKey)
	insertLiveConnection(t, b, box, "a", time.Now().Add(time.Hour))
	realm := "4620816365" + org[:4]

	body := []byte(`{"eventNotifications":[{"realmId":"` + realm + `","dataChangeEvent":{"entities":[{"name":"Invoice","id":"145","operation":"Update"}]}},{"realmId":"unknown-realm"}]}`)
	mac := hmac.New(sha256.New, []byte("verifier-secret"))
	mac.Write(body)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	anon := env.Client()
	post := func(signature string) apitest.Response {
		req, _ := http.NewRequest(http.MethodPost, "http://finops.test/api/webhooks/quickbooks", bytes.NewReader(body))
		req.Header.Set("intuit-signature", signature)
		req.Header.Set("Content-Type", "application/json")
		return anon.Raw(req)
	}
	if r := post("bm90LXZhbGlk"); r.Status != http.StatusUnauthorized {
		t.Fatalf("bad signature must be rejected, got %d", r.Status)
	}
	r := post(sig)
	if r.Status != http.StatusOK || r.Map(t)["queued"].(float64) != 1 {
		t.Fatalf("expected one queued job, got %d %s", r.Status, r.Body)
	}
	if r := post(sig); r.Map(t)["queued"].(float64) != 0 {
		t.Fatal("a redelivered webhook must not queue a second job")
	}
	if n := countRows(t, b, "SELECT count(*) FROM sync_jobs WHERE organization_id = $1 AND trigger = 'webhook'"); n != 1 {
		t.Fatalf("expected one webhook job, got %d", n)
	}
}

func TestSyncAndReconciliationAreIsolatedAndPermissioned(t *testing.T) {
	b := booksWithInvoices(t, 3)
	b.connect(t)
	b.owner.Must(http.StatusAccepted, b.owner.Post(b.url("/sync-jobs"), map[string]string{"kind": "full"}))
	b.env.RunJobs()

	viewer := b.env.Client()
	b.owner.AddMember(b.org, viewer.Register("Viewer"), "viewer")
	for _, path := range []string{"/integrations/quickbooks/connect", "/sync-jobs", "/reconciliation/run"} {
		if r := viewer.Post(b.url(path), map[string]string{"kind": "full"}); r.Status != http.StatusForbidden {
			t.Errorf("viewer POST %s: expected 403, got %d", path, r.Status)
		}
	}

	other := b.env.Client()
	other.Register("Other")
	org := other.CreateOrg("Other", "USD")
	var jobs struct {
		Total int `json:"total"`
	}
	other.Must(http.StatusOK, other.Get("/api/orgs/"+org+"/sync-jobs")).JSON(t, &jobs)
	var rec struct {
		Total int `json:"total"`
	}
	other.Must(http.StatusOK, other.Get("/api/orgs/"+org+"/reconciliation")).JSON(t, &rec)
	var ext struct {
		Data []any `json:"data"`
	}
	other.Must(http.StatusOK, other.Get("/api/orgs/"+org+"/external-records")).JSON(t, &ext)
	if jobs.Total != 0 || rec.Total != 0 || len(ext.Data) != 0 {
		t.Fatalf("another organization must see nothing: jobs=%d rec=%d ext=%d", jobs.Total, rec.Total, len(ext.Data))
	}
	if r := other.Get("/api/orgs/" + b.org + "/sync-jobs"); r.Status != http.StatusNotFound {
		t.Fatalf("foreign org sync jobs: expected 404, got %d", r.Status)
	}
}
