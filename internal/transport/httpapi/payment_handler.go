package httpapi

import (
	"errors"
	"net/http"
	"time"

	"example.com/order-system/internal/domain/payment"
)

type paymentHandler struct {
	queries PaymentQueries
}

func newPaymentHandler(queries PaymentQueries) *paymentHandler {
	return &paymentHandler{queries: queries}
}

type paymentResponse struct {
	ID          string         `json:"id"`
	OrderID     string         `json:"order_id"`
	AmountCents int64          `json:"amount_cents"`
	Status      payment.Status `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

func (h *paymentHandler) getByOrderID(w http.ResponseWriter, r *http.Request) {
	customerID, ok := AuthenticatedUserID(r.Context())
	if !ok {
		writeUnauthorized(w)
		return
	}

	got, err := h.queries.GetPayment(r.Context(), customerID, r.PathValue("orderID"))
	if errors.Is(err, payment.ErrPaymentNotFound) {
		writeError(w, http.StatusNotFound, "payment not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	_ = writeJSON(w, http.StatusOK, paymentResponse{
		ID: got.ID(), OrderID: got.OrderID(), AmountCents: got.AmountCents(),
		Status: got.Status(), CreatedAt: got.CreatedAt(), UpdatedAt: got.UpdatedAt(),
	})
}
