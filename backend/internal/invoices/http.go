package invoices

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"finopshq/internal/authz"
	"finopshq/internal/httpx"
	"finopshq/internal/input"
	"finopshq/internal/store"
)

type Handler struct{ svc *Service }

func NewHandler(s *Service) *Handler { return &Handler{svc: s} }

var listStatuses = []string{"draft", "sent", "partially_paid", "paid", "void", "overdue", "outstanding"}

func (h *Handler) InvoiceRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Post("/", httpx.Handle(h.create))
	r.Post("/preview", httpx.Handle(h.preview))
	r.Get("/{id}", httpx.Handle(h.get))
	r.Patch("/{id}", httpx.Handle(h.update))
	r.Post("/{id}/send", httpx.Handle(h.send))
	r.Post("/{id}/void", httpx.Handle(h.void))
	r.Post("/{id}/payments", h.recordPayment)
}

func (h *Handler) PaymentRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listPayments))
	r.Post("/{id}/reverse", httpx.Handle(h.reversePayment))
}

func (h *Handler) list(_ http.ResponseWriter, r *http.Request) (int, any, error) {
	status, err := input.QueryEnum(r, "status", listStatuses...)
	if err != nil {
		return 0, nil, err
	}
	cust, err := input.QueryUUID(r, "customer_id")
	if err != nil {
		return 0, nil, err
	}
	proj, err := input.QueryUUID(r, "project_id")
	if err != nil {
		return 0, nil, err
	}
	from, to, err := input.DateRange(r)
	if err != nil {
		return 0, nil, err
	}
	page := input.PageFrom(r)
	res, err := h.svc.List(r.Context(), authz.MustPrincipal(r.Context()), store.ListInvoicesParams{
		Status: status, CustomerID: cust, ProjectID: proj, Pattern: input.Pattern(r.URL.Query().Get("q")),
		From: from, To: to, RowLimit: page.Limit, RowOffset: page.Offset,
	})
	return http.StatusOK, res, err
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) (int, any, error) {
	in, err := httpx.Decode[Input](w, r)
	if err != nil {
		return 0, nil, err
	}
	inv, err := h.svc.Create(r.Context(), authz.MustPrincipal(r.Context()), in)
	return http.StatusCreated, map[string]any{"invoice": inv}, err
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) (int, any, error) {
	in, err := httpx.Decode[Input](w, r)
	if err != nil {
		return 0, nil, err
	}
	res, err := h.svc.Preview(authz.MustPrincipal(r.Context()), in)
	return http.StatusOK, res, err
}

func (h *Handler) get(_ http.ResponseWriter, r *http.Request) (int, any, error) {
	id, err := httpx.URLParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	inv, err := h.svc.Get(r.Context(), authz.MustPrincipal(r.Context()), id)
	return http.StatusOK, map[string]any{"invoice": inv}, err
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) (int, any, error) {
	id, err := httpx.URLParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	in, err := httpx.Decode[Input](w, r)
	if err != nil {
		return 0, nil, err
	}
	inv, err := h.svc.Update(r.Context(), authz.MustPrincipal(r.Context()), id, in)
	return http.StatusOK, map[string]any{"invoice": inv}, err
}

type actionInput struct {
	Reason  string `json:"reason"`
	Version *int32 `json:"version"`
}

func (h *Handler) send(w http.ResponseWriter, r *http.Request) (int, any, error) {
	id, err := httpx.URLParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var in actionInput
	if r.ContentLength != 0 {
		if in, err = httpx.Decode[actionInput](w, r); err != nil {
			return 0, nil, err
		}
	}
	inv, err := h.svc.Send(r.Context(), authz.MustPrincipal(r.Context()), id, in.Version)
	return http.StatusOK, map[string]any{"invoice": inv}, err
}

func (h *Handler) void(w http.ResponseWriter, r *http.Request) (int, any, error) {
	id, err := httpx.URLParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	in, err := httpx.Decode[actionInput](w, r)
	if err != nil {
		return 0, nil, err
	}
	inv, err := h.svc.Void(r.Context(), authz.MustPrincipal(r.Context()), id, in.Reason, in.Version)
	return http.StatusOK, map[string]any{"invoice": inv}, err
}

func (h *Handler) recordPayment(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.URLParamUUID(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	in, err := httpx.Decode[PaymentInput](w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.svc.RecordPayment(r.Context(), authz.MustPrincipal(r.Context()), id, r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	status := http.StatusCreated
	if res.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, res)
}

func (h *Handler) listPayments(_ http.ResponseWriter, r *http.Request) (int, any, error) {
	from, to, err := input.DateRange(r)
	if err != nil {
		return 0, nil, err
	}
	method, err := input.QueryEnum(r, "method", paymentMethods...)
	if err != nil {
		return 0, nil, err
	}
	status, err := input.QueryEnum(r, "status", "completed", "reversed")
	if err != nil {
		return 0, nil, err
	}
	page := input.PageFrom(r)
	res, err := h.svc.ListPayments(r.Context(), authz.MustPrincipal(r.Context()), store.ListPaymentsParams{
		From: from, To: to, Method: method, Status: status, Pattern: input.Pattern(r.URL.Query().Get("q")),
		RowLimit: page.Limit, RowOffset: page.Offset,
	})
	return http.StatusOK, res, err
}

func (h *Handler) reversePayment(w http.ResponseWriter, r *http.Request) (int, any, error) {
	id, err := httpx.URLParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	in, err := httpx.Decode[actionInput](w, r)
	if err != nil {
		return 0, nil, err
	}
	res, err := h.svc.ReversePayment(r.Context(), authz.MustPrincipal(r.Context()), id, in.Reason)
	return http.StatusOK, res, err
}
