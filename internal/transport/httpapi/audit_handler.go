package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"example.com/order-system/internal/application/audit"
)

type AuditQueries interface {
	List(context.Context, int) ([]audit.Event, error)
}

type auditHandler struct{ queries AuditQueries }

func (h auditHandler) list(w http.ResponseWriter, r *http.Request) {
	limit := audit.DefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > audit.MaxLimit {
			writeError(w, http.StatusBadRequest, errorCodeInvalidAuditQuery, "audit limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	events, err := h.queries.List(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternalError, "internal server error")
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{"items": events})
}
