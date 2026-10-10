package orderapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"example.com/order-system/internal/domain/order"
)

const (
	DefaultListLimit = 20
	MaxListLimit     = 100
	maxCursorLength  = 1024
	cursorVersion    = 1
)

var (
	ErrInvalidListLimit  = errors.New("order list limit must be between 1 and 100")
	ErrInvalidListCursor = errors.New("invalid order list cursor")
)

// PageBoundary 是持久化适配器执行 Keyset Pagination 所需的排序边界。
// 下一页只读取严格排在这个 (created_at, id) 组合之后的订单。
type PageBoundary struct {
	CreatedAt time.Time
	OrderID   string
}

type ListOrdersQuery struct {
	CustomerID string
	Limit      int
	Cursor     string
}

type ListOrdersResult struct {
	Orders     []order.Order
	NextCursor string
}

type cursorPayload struct {
	Version   int    `json:"v"`
	CreatedAt string `json:"created_at"`
	OrderID   string `json:"order_id"`
}

// ListOrders 返回当前用户的订单历史。游标由应用层编解码，HTTP 客户端
// 不需要知道数据库排序字段，持久化适配器也不需要依赖 HTTP 字符串格式。
func (s *Service) ListOrders(ctx context.Context, query ListOrdersQuery) (ListOrdersResult, error) {
	query, boundary, err := normalizeListOrdersQuery(query)
	if err != nil {
		return ListOrdersResult{}, err
	}

	// 多取一条只用于判断是否还有下一页，不把额外记录返回给客户端。
	records, err := s.store.ListOrdersByCustomerID(
		ctx, query.CustomerID, boundary, query.Limit+1,
	)
	if err != nil {
		return ListOrdersResult{}, fmt.Errorf("list orders: %w", err)
	}

	result := ListOrdersResult{Orders: records}
	if len(records) <= query.Limit {
		return result, nil
	}

	result.Orders = records[:query.Limit]
	last := result.Orders[len(result.Orders)-1]
	result.NextCursor, err = encodeOrderCursor(PageBoundary{
		CreatedAt: last.CreatedAt(),
		OrderID:   last.ID(),
	})
	if err != nil {
		return ListOrdersResult{}, fmt.Errorf("encode next order cursor: %w", err)
	}

	return result, nil
}

func normalizeListOrdersQuery(query ListOrdersQuery) (ListOrdersQuery, *PageBoundary, error) {
	query.CustomerID = strings.TrimSpace(query.CustomerID)
	query.Cursor = strings.TrimSpace(query.Cursor)
	if query.CustomerID == "" {
		return ListOrdersQuery{}, nil, order.ErrInvalidCustomerID
	}
	if query.Limit == 0 {
		query.Limit = DefaultListLimit
	}
	if query.Limit < 1 || query.Limit > MaxListLimit {
		return ListOrdersQuery{}, nil, ErrInvalidListLimit
	}

	boundary, err := decodeOrderCursor(query.Cursor)
	if err != nil {
		return ListOrdersQuery{}, nil, err
	}
	return query, boundary, nil
}

func encodeOrderCursor(boundary PageBoundary) (string, error) {
	payload, err := json.Marshal(cursorPayload{
		Version:   cursorVersion,
		CreatedAt: boundary.CreatedAt.UTC().Format(time.RFC3339Nano),
		OrderID:   boundary.OrderID,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeOrderCursor(encoded string) (*PageBoundary, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > maxCursorLength {
		return nil, ErrInvalidListCursor
	}

	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrInvalidListCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload cursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, ErrInvalidListCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidListCursor
	}

	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil || createdAt.IsZero() || payload.Version != cursorVersion ||
		strings.TrimSpace(payload.OrderID) == "" {
		return nil, ErrInvalidListCursor
	}

	return &PageBoundary{
		CreatedAt: createdAt.UTC(),
		OrderID:   payload.OrderID,
	}, nil
}
