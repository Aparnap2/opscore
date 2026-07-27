package main

import (
	"net/http"
	"strings"

	"github.com/aparna/opscore/internal/middleware/tenant"
)

// signalsHandler handles GET /signals/{id}.
//
// It probes the dedicated manufacturing tables (PO -> GRN -> Invoice) in order
// and returns the first match with a `document_type` discriminator. Tenant
// isolation is enforced by every Get*ByID call (ErrNotFound for other tenants).
// Thin handler: no business logic.
func (s *ServerDeps) signalsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	t := tenant.FromContext(r.Context())
	tenantID := t.ID

	id := strings.TrimPrefix(r.URL.Path, "/signals/")
	id = strings.TrimRight(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Missing signal ID")
		return
	}

	ctx := r.Context()

	if po, err := s.db.GetPurchaseOrderByID(ctx, id, tenantID); err == nil && po != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"document_type": "PURCHASE_ORDER",
			"id":            po.ID,
			"data":          po,
		})
		return
	}
	if gr, err := s.db.GetGoodsReceiptByID(ctx, id, tenantID); err == nil && gr != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"document_type": "GOODS_RECEIPT",
			"id":            gr.ID,
			"data":          gr,
		})
		return
	}
	if inv, err := s.db.GetInvoiceByID(ctx, id, tenantID); err == nil && inv != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"document_type": "INVOICE",
			"id":            inv.ID,
			"data":          inv,
		})
		return
	}

	writeError(w, http.StatusNotFound, "Signal not found")
}
