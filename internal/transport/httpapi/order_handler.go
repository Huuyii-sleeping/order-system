package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/domain/order"
)

const (
	IdempotencyKeyHeader = "Idempotency-Key"
)

type orderHandler struct {
	useCases OrderUseCases
}

func newOrderHandler(useCases OrderUseCases) *orderHandler {
	return &orderHandler{useCases: useCases}
}

type createOrderRequest struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

type orderResponse struct {
	ID             string       `json:"id"`
	CustomerID     string       `json:"customer_id"`
	ProductID      string       `json:"product_id"`
	Quantity       int          `json:"quantity"`
	UnitPriceCents int64        `json:"unit_price_cents"`
	TotalCents     int64        `json:"total_cents"`
	Status         order.Status `json:"status"`
	CreatedAt      time.Time    `json:"created_at"`
}

type orderListResponse struct {
	Items      []orderResponse `json:"items"`
	NextCursor string          `json:"next_cursor"`
}

func (h *orderHandler) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := AuthenticatedUserID(r.Context())
	if !ok {
		writeUnauthorized(w)
		return
	}

	if !hasJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, errorCodeContentTypeRequired, "content type must be application/json")
		return
	}

	var request createOrderRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeJSONBodyError(w, err)
		return
	}

	result, err := h.useCases.CreateOrder(r.Context(), orderapp.CreateOrderCommand{
		CustomerID:     userID,
		ProductID:      request.ProductID,
		Quantity:       request.Quantity,
		IdempotencyKey: r.Header.Get(IdempotencyKeyHeader),
	})
	if err != nil {
		writeCreateOrderError(w, err)
		return
	}

	statusCode := http.StatusOK
	if result.Created {
		statusCode = http.StatusCreated
		w.Header().Set("Location", "/orders/"+result.Order.ID())
	}

	_ = writeJSON(w, statusCode, orderToResponse(result.Order))
}

func (h *orderHandler) getByID(w http.ResponseWriter, r *http.Request) {
	userID, ok := AuthenticatedUserID(r.Context())
	if !ok {
		writeUnauthorized(w)
		return
	}

	got, err := h.useCases.GetOrder(r.Context(), userID, r.PathValue("orderID"))
	if err != nil {
		if errors.Is(err, order.ErrOrderNotFound) {
			writeError(w, http.StatusNotFound, errorCodeOrderNotFound, "order not found")
			return
		}

		writeError(w, http.StatusInternalServerError, errorCodeInternalError, "internal server error")
		return
	}

	_ = writeJSON(w, http.StatusOK, orderToResponse(got))
}

func (h *orderHandler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := AuthenticatedUserID(r.Context())
	if !ok {
		writeUnauthorized(w)
		return
	}

	query, err := parseOrderListQuery(r, userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, errorCodeInvalidOrderListQuery, "invalid order list query")
		return
	}

	result, err := h.useCases.ListOrders(r.Context(), query)
	if err != nil {
		switch {
		case errors.Is(err, orderapp.ErrInvalidListLimit), errors.Is(err, orderapp.ErrInvalidListCursor):
			writeError(w, http.StatusBadRequest, errorCodeInvalidOrderListQuery, "invalid order list query")
		default:
			writeError(w, http.StatusInternalServerError, errorCodeInternalError, "internal server error")
		}
		return
	}

	items := make([]orderResponse, 0, len(result.Orders))
	for _, current := range result.Orders {
		items = append(items, orderToResponse(current))
	}
	_ = writeJSON(w, http.StatusOK, orderListResponse{
		Items: items, NextCursor: result.NextCursor,
	})
}

func parseOrderListQuery(r *http.Request, customerID string) (orderapp.ListOrdersQuery, error) {
	values := r.URL.Query()
	for key := range values {
		if key != "limit" && key != "cursor" {
			return orderapp.ListOrdersQuery{}, orderapp.ErrInvalidListCursor
		}
	}
	if len(values["limit"]) > 1 || len(values["cursor"]) > 1 {
		return orderapp.ListOrdersQuery{}, orderapp.ErrInvalidListLimit
	}

	limit := orderapp.DefaultListLimit
	if rawLimit, present := values["limit"]; present {
		parsed, err := strconv.Atoi(rawLimit[0])
		if err != nil || parsed < 1 || parsed > orderapp.MaxListLimit {
			return orderapp.ListOrdersQuery{}, orderapp.ErrInvalidListLimit
		}
		limit = parsed
	}

	return orderapp.ListOrdersQuery{
		CustomerID: customerID,
		Limit:      limit,
		Cursor:     values.Get("cursor"),
	}, nil
}

func writeCreateOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, order.ErrInvalidProductID):
		writeError(w, http.StatusBadRequest, errorCodeProductIDRequired, "product_id is required")
	case errors.Is(err, order.ErrInvalidQuantity):
		writeError(w, http.StatusBadRequest, errorCodeInvalidQuantity, "quantity must be greater than zero")
	case errors.Is(err, order.ErrInvalidIdempotencyKey):
		writeError(w, http.StatusBadRequest, errorCodeIdempotencyKeyRequired, "idempotency key is required")
	case errors.Is(err, catalog.ErrProductNotFound):
		writeError(w, http.StatusNotFound, errorCodeProductNotFound, "product not found")
	case errors.Is(err, inventory.ErrInsufficientStock):
		writeError(w, http.StatusConflict, errorCodeInsufficientStock, "insufficient stock")
	case errors.Is(err, orderapp.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, errorCodeIdempotencyConflict, "idempotency key conflict")
	default:
		writeError(w, http.StatusInternalServerError, errorCodeInternalError, "internal server error")
	}
}

func orderToResponse(created order.Order) orderResponse {
	return orderResponse{
		ID:             created.ID(),
		CustomerID:     created.CustomerID(),
		ProductID:      created.ProductID(),
		Quantity:       created.Quantity(),
		UnitPriceCents: created.UnitPriceCents(),
		TotalCents:     created.TotalCents(),
		Status:         created.Status(),
		CreatedAt:      created.CreatedAt(),
	}
}

func hasJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}
