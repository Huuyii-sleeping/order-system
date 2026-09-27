package httpapi

import (
	"errors"
	"net/http"

	"example.com/order-system/internal/domain/catalog"
)

type catalogHandler struct {
	queries CatalogQueries
}

func newCatalogHandler(queries CatalogQueries) *catalogHandler {
	return &catalogHandler{queries: queries}
}

// productResponse 是 HTTP 层对外暴露的数据结构。
//
// 它与领域 Product 分开，避免 JSON 字段、版本兼容等协议要求
// 反向污染领域模型。
type productResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PriceCents int64  `json:"price_cents"`
}

type productListResponse struct {
	Items []productResponse `json:"items"`
}

func (h *catalogHandler) list(w http.ResponseWriter, r *http.Request) {
	products, err := h.queries.ListProducts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	items := make([]productResponse, 0, len(products))
	for _, product := range products {
		items = append(items, productToResponse(product))
	}

	_ = writeJSON(w, http.StatusOK, productListResponse{Items: items})
}

func (h *catalogHandler) getByID(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("productID")
	product, err := h.queries.GetProduct(r.Context(), productID)
	if err != nil {
		if errors.Is(err, catalog.ErrProductNotFound) {
			writeError(w, http.StatusNotFound, "product not found")
			return
		}

		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	_ = writeJSON(w, http.StatusOK, productToResponse(product))
}

func productToResponse(product catalog.Product) productResponse {
	return productResponse{
		ID:         product.ID(),
		Name:       product.Name(),
		PriceCents: product.PriceCents(),
	}
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	_ = writeJSON(w, statusCode, map[string]string{
		"error": message,
	})
}
