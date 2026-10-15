package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const (
	// MaxJSONBodyBytes protects JSON endpoints from unbounded decoder memory use.
	// It is deliberately shared by authenticated business requests and signed
	// webhooks so every JSON entry point has the same baseline boundary.
	maxJSONBodyBytes = 1 << 20

	// DefaultMaxHeaderBytes limits request-line and header memory before a
	// request reaches any Router or application use case.
	DefaultMaxHeaderBytes = 1 << 20
)

var errRequestBodyTooLarge = errors.New("request body exceeds the configured limit")

// decodeJSONBody reads exactly one JSON value and rejects unknown fields.
// MaxBytesReader enforces the limit while streaming, so a chunked request
// cannot bypass the boundary by omitting Content-Length.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(destination); err != nil {
		if isRequestBodyTooLarge(err) {
			return errRequestBodyTooLarge
		}
		return fmt.Errorf("decode JSON: %w", err)
	}

	// 一个请求体只允许包含一个 JSON 值，避免后面拼接的内容被忽略。
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if isRequestBodyTooLarge(err) {
			return errRequestBodyTooLarge
		}
		return errors.New("request body must contain one JSON object")
	}

	return nil
}

func isRequestBodyTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

func writeJSONBodyError(w http.ResponseWriter, err error) {
	if errors.Is(err, errRequestBodyTooLarge) {
		writeError(
			w,
			http.StatusRequestEntityTooLarge,
			errorCodeRequestBodyTooLarge,
			"request body is too large",
		)
		return
	}
	writeError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
}
