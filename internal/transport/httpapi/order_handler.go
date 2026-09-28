package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/domain/order"
)

const (
	IdempotencyKeyHeader = "Idempotency-Key"
	maxJSONBodyBytes     = 1 << 20
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

func (h *orderHandler) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := AuthenticatedUserID(r.Context())
	if !ok {
		writeUnauthorized(w)
		return
	}

	if !hasJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}

	var request createOrderRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
			writeError(w, http.StatusNotFound, "order not found")
			return
		}

		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	_ = writeJSON(w, http.StatusOK, orderToResponse(got))
}

func writeCreateOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, order.ErrInvalidProductID):
		writeError(w, http.StatusBadRequest, "product_id is required")
	case errors.Is(err, order.ErrInvalidQuantity):
		writeError(w, http.StatusBadRequest, "quantity must be greater than zero")
	case errors.Is(err, order.ErrInvalidIdempotencyKey):
		writeError(w, http.StatusBadRequest, "idempotency key is required")
	case errors.Is(err, catalog.ErrProductNotFound):
		writeError(w, http.StatusNotFound, "product not found")
	case errors.Is(err, inventory.ErrInsufficientStock):
		writeError(w, http.StatusConflict, "insufficient stock")
	case errors.Is(err, orderapp.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency key conflict")
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
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

func decodeJSONBody(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}

	// 一个请求体只允许包含一个 JSON 值，避免后面拼接的内容被忽略。
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}

	return nil
}
