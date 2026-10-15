package httpapi

import "net/http"

const (
	errorCodeInternalError           = "internal_error"
	errorCodeContentTypeRequired     = "content_type_required"
	errorCodeInvalidRequestBody      = "invalid_request_body"
	errorCodeRequestBodyTooLarge     = "request_body_too_large"
	errorCodeInvalidBearerToken      = "invalid_bearer_token"
	errorCodeServiceUnavailable      = "service_unavailable"
	errorCodeRateLimited             = "rate_limited"
	errorCodeProductNotFound         = "product_not_found"
	errorCodeInvalidEmail            = "invalid_email"
	errorCodeInvalidPassword         = "invalid_password"
	errorCodeEmailAlreadyExists      = "email_already_exists"
	errorCodeInvalidCredentials      = "invalid_credentials"
	errorCodeInvalidCSRFToken        = "invalid_csrf_token"
	errorCodeInvalidRefreshToken     = "invalid_refresh_token"
	errorCodeOrderNotFound           = "order_not_found"
	errorCodeInvalidOrderListQuery   = "invalid_order_list_query"
	errorCodeProductIDRequired       = "product_id_required"
	errorCodeInvalidQuantity         = "invalid_quantity"
	errorCodeIdempotencyKeyRequired  = "idempotency_key_required"
	errorCodeInsufficientStock       = "insufficient_stock"
	errorCodeIdempotencyConflict     = "idempotency_key_conflict"
	errorCodePaymentNotFound         = "payment_not_found"
	errorCodeNotificationNotFound    = "notification_not_found"
	errorCodeInvalidWebhook          = "invalid_webhook_event"
	errorCodeInvalidWebhookSignature = "invalid_webhook_signature"
	errorCodeWebhookConflict         = "webhook_event_conflict"
)

// errorResponse 同时保留旧的 error 文案和新的稳定 code。
//
// 客户端应根据 code 分支，error 只用于展示和诊断。保留 error 字段让已经
// 依赖旧响应的客户端继续工作，避免为了改进契约制造一次不必要的破坏性变更。
type errorResponse struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, statusCode int, code, message string) {
	_ = writeJSON(w, statusCode, errorResponse{Code: code, Error: message})
}
