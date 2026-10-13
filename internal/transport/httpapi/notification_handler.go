package httpapi

import (
	"errors"
	"net/http"
	"time"

	domainnotification "example.com/order-system/internal/domain/notification"
)

type notificationHandler struct{ queries NotificationQueries }

func newNotificationHandler(queries NotificationQueries) *notificationHandler {
	return &notificationHandler{queries: queries}
}

type notificationResponse struct {
	ID        string                    `json:"id"`
	OrderID   string                    `json:"order_id"`
	EventType string                    `json:"event_type"`
	Status    domainnotification.Status `json:"status"`
	CreatedAt time.Time                 `json:"created_at"`
	UpdatedAt time.Time                 `json:"updated_at"`
}

func (h *notificationHandler) getByOrderID(w http.ResponseWriter, r *http.Request) {
	customerID, ok := AuthenticatedUserID(r.Context())
	if !ok {
		writeUnauthorized(w)
		return
	}
	got, err := h.queries.GetNotification(r.Context(), customerID, r.PathValue("orderID"))
	if errors.Is(err, domainnotification.ErrNotificationNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotificationNotFound, "notification not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternalError, "internal server error")
		return
	}
	_ = writeJSON(w, http.StatusOK, notificationResponse{ID: got.ID(), OrderID: got.OrderID(), EventType: got.EventType(), Status: got.Status(), CreatedAt: got.CreatedAt(), UpdatedAt: got.UpdatedAt()})
}
