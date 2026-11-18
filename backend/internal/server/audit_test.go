package server_test

import (
	"net/http"
	"net/url"
	"testing"
)

func TestAuditLogEndpoint(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.sentInvoice(t, "120.00").Invoice
	b.owner.Must(http.StatusCreated, b.pay(b.owner, inv.ID, "20.00", "audit-pay-key"))

	viewer := b.env.Client()
	b.owner.AddMember(b.org, viewer.Register("Viewer"), "viewer")
	if r := viewer.Get(b.url("/audit-logs")); r.Status != http.StatusForbidden {
		t.Fatalf("viewers cannot read audit history, got %d", r.Status)
	}

	type page struct {
		Data []struct {
			Action    string `json:"action"`
			ActorName string `json:"actor_name"`
			Changes   struct {
				After map[string]any `json:"after"`
			} `json:"changes"`
		} `json:"data"`
		NextCursor *string `json:"next_cursor"`
	}
	var p page
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/audit-logs?entity_type=payment"))).JSON(t, &p)
	if len(p.Data) != 1 || p.Data[0].Action != "payment.recorded" || p.Data[0].ActorName != "Owner" || p.Data[0].Changes.After["amount"] != "20.00" {
		t.Fatalf("payment audit entry %+v", p.Data)
	}

	var all page
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/audit-logs"))).JSON(t, &all)
	if len(all.Data) < 6 || all.NextCursor != nil {
		t.Fatalf("expected a single page of audit entries, got %d", len(all.Data))
	}
	for i := 0; i < 55; i++ {
		b.owner.Must(http.StatusCreated, b.owner.Post(b.url("/customers"), map[string]string{"name": "Bulk customer"}))
	}
	var first, second page
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/audit-logs"))).JSON(t, &first)
	if first.NextCursor == nil || len(first.Data) != 50 {
		t.Fatalf("first page should be full with a cursor, got %d", len(first.Data))
	}
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/audit-logs?cursor="+url.QueryEscape(*first.NextCursor)))).JSON(t, &second)
	if len(second.Data) == 0 || len(first.Data)+len(second.Data) != len(all.Data)+55 {
		t.Fatalf("cursor paging lost or duplicated entries: %d + %d vs %d", len(first.Data), len(second.Data), len(all.Data)+55)
	}
	if r := b.owner.Get(b.url("/audit-logs?cursor=garbage")); r.Status != http.StatusBadRequest {
		t.Fatalf("bad cursor should be rejected, got %d", r.Status)
	}
}
