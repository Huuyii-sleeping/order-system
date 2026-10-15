package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	orderapp "example.com/order-system/internal/application/order"
)

// FuzzDecodeJSONBodyNeverPanics uses arbitrary bytes to exercise the same
// decoder used by register, login and create-order handlers. The invariant is
// that malformed or oversized input is reported as an error, never a panic.
func FuzzDecodeJSONBodyNeverPanics(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"product_id":"product-1","quantity":1}`,
		`{"unknown":true}`,
		`{`,
		strings.Repeat("x", maxJSONBodyBytes+1),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, body string) {
		request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body))
		response := httptest.NewRecorder()
		var destination createOrderRequest

		// Arbitrary input may be valid, malformed, unknown-field JSON or an
		// oversized body. Every outcome must stay inside this transport helper;
		// the important property is that it never panics or calls a UseCase.
		_ = decodeJSONBody(response, request, &destination)
	})
}

// FuzzParseOrderListQuery verifies that arbitrary query strings cannot panic
// the keyset-pagination parser. A successful parse must still preserve the
// authenticated customer and configured page-size bounds.
func FuzzParseOrderListQueryKeepsPaginationBounds(f *testing.F) {
	for _, seed := range []string{
		"",
		"limit=20",
		"limit=0",
		"cursor=next-page-token",
		"limit=20&cursor=next-page-token",
		"%zz",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawQuery string) {
		request := httptest.NewRequest(http.MethodGet, "/orders", nil)
		request.URL.RawQuery = rawQuery

		query, err := parseOrderListQuery(request, "customer-1")
		if err != nil {
			return
		}
		if query.CustomerID != "customer-1" {
			t.Fatalf("customer ID = %q, want authenticated customer", query.CustomerID)
		}
		if query.Limit < 1 || query.Limit > orderapp.MaxListLimit {
			t.Fatalf("limit = %d, outside configured bounds", query.Limit)
		}
	})
}
