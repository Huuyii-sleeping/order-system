// Package httpapi 负责把 HTTP 请求转换为应用能够处理的输入，
// 并把处理结果转换为 HTTP 响应。
//
// 这个包属于传输层，不应该包含订单、库存等核心业务规则。
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"example.com/order-system/api/openapi"
	authapp "example.com/order-system/internal/application/auth"
	"example.com/order-system/internal/application/authorization"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/identity"
	domainnotification "example.com/order-system/internal/domain/notification"
	"example.com/order-system/internal/domain/order"
	"example.com/order-system/internal/domain/payment"
)

// CatalogQueries 是 HTTP 商品接口需要的应用能力。
//
// 接口定义在消费方 httpapi 中，便于测试时替换实现，
// 同时避免 HTTP 层依赖具体的应用 Service 类型。
type CatalogQueries interface {
	ListProducts(context.Context) ([]catalog.Product, error)
	GetProduct(context.Context, string) (catalog.Product, error)
}

// OrderUseCases 是订单 HTTP 接口使用的应用能力。
type OrderUseCases interface {
	CreateOrder(context.Context, orderapp.CreateOrderCommand) (orderapp.CreateOrderResult, error)
	GetOrder(context.Context, string, string) (order.Order, error)
	ListOrders(context.Context, orderapp.ListOrdersQuery) (orderapp.ListOrdersResult, error)
}

type PaymentQueries interface {
	GetPayment(context.Context, string, string) (payment.Payment, error)
}

type NotificationQueries interface {
	GetNotification(context.Context, string, string) (domainnotification.Notification, error)
}

type AuthUseCases interface {
	Register(context.Context, authapp.RegisterCommand) (identity.User, error)
	Login(context.Context, authapp.LoginCommand) (authapp.LoginResult, error)
	Refresh(context.Context, string) (authapp.RefreshResult, error)
	Authenticate(context.Context, string) (string, error)
	Logout(context.Context, string) error
	LogoutAll(context.Context, string) error
}

type SessionAuthenticator interface {
	Authenticate(context.Context, string) (string, error)
}

type AdministrativeAuth interface {
	PrincipalAuthenticator
}

type AuthSecurityConfig struct {
	SecureCookies  bool
	LoginLimiter   RequestRateLimiter
	RefreshLimiter RequestRateLimiter
}

type ReadinessChecker interface {
	CheckReadiness(context.Context) error
}

type RouterConfig struct {
	AuthSecurity                AuthSecurityConfig
	Readiness                   ReadinessChecker
	ReadinessTimeout            time.Duration
	MetricsHandler              http.Handler
	PaymentQueries              PaymentQueries
	NotificationQueries         NotificationQueries
	NotificationWebhooks        NotificationWebhookUseCases
	NotificationWebhookSecurity NotificationWebhookSecurity
	NotificationWebhookMetrics  NotificationWebhookMetrics
	Logger                      *slog.Logger
	Authorization               PermissionAuthorizer
	Audit                       interface {
		AuditRecorder
		AuditQueries
	}
}

// NewRouter 创建 API 使用的 HTTP 路由。
//
// 返回 http.Handler 而不是具体的 *http.ServeMux，
// 可以让调用方只依赖标准接口。以后在路由外包装中间件时，
// main.go 也不需要改变接收类型。
func NewRouter(
	catalogQueries CatalogQueries,
	authUseCases AuthUseCases,
	orderUseCases OrderUseCases,
) http.Handler {
	return NewRouterWithAuthSecurity(
		catalogQueries, authUseCases, orderUseCases, AuthSecurityConfig{},
	)
}

func NewRouterWithAuthSecurity(
	catalogQueries CatalogQueries,
	authUseCases AuthUseCases,
	orderUseCases OrderUseCases,
	security AuthSecurityConfig,
) http.Handler {
	return NewRouterWithConfig(
		catalogQueries, authUseCases, orderUseCases,
		RouterConfig{AuthSecurity: security},
	)
}

func NewRouterWithConfig(
	catalogQueries CatalogQueries,
	authUseCases AuthUseCases,
	orderUseCases OrderUseCases,
	config RouterConfig,
) http.Handler {
	mux := http.NewServeMux()

	// OpenAPI 文档与 CI 检查读取的是同一份嵌入文件，避免线上说明书和
	// 仓库中接受评审的契约来自两套来源。
	mux.Handle("GET /openapi.json", openapi.Handler())

	// Go 1.22 之后可以在路由模式中同时声明 Method 和 Path。
	// 因此 POST /healthz 会由标准库自动返回 405 Method Not Allowed。
	mux.HandleFunc("GET /healthz", healthHandler)
	mux.Handle("GET /readyz", newReadinessHandler(
		config.Readiness, config.ReadinessTimeout, config.Logger,
	))
	if config.MetricsHandler != nil {
		mux.Handle("GET /metrics", config.MetricsHandler)
	}

	catalogHandler := newCatalogHandler(catalogQueries)
	mux.HandleFunc("GET /products", catalogHandler.list)
	mux.HandleFunc("GET /products/{productID}", catalogHandler.getByID)

	security := config.AuthSecurity
	authHandler := newAuthHandler(authUseCases, security.SecureCookies)
	mux.HandleFunc("POST /auth/register", authHandler.register)
	mux.Handle("POST /auth/login", WithRateLimit(
		security.LoginLimiter, http.HandlerFunc(authHandler.login),
	))
	mux.Handle("POST /auth/refresh", WithRateLimit(
		security.RefreshLimiter, http.HandlerFunc(authHandler.refresh),
	))
	mux.Handle("POST /auth/logout", WithAuthentication(authUseCases, http.HandlerFunc(authHandler.logout)))
	mux.Handle("POST /auth/logout-all", WithAuthentication(authUseCases, http.HandlerFunc(authHandler.logoutAll)))

	orderHandler := newOrderHandler(orderUseCases)
	mux.Handle("POST /orders", WithAuthentication(authUseCases, http.HandlerFunc(orderHandler.create)))
	mux.Handle("GET /orders", WithAuthentication(authUseCases, http.HandlerFunc(orderHandler.list)))
	mux.Handle("GET /orders/{orderID}", WithAuthentication(authUseCases, http.HandlerFunc(orderHandler.getByID)))
	if config.PaymentQueries != nil {
		paymentHandler := newPaymentHandler(config.PaymentQueries)
		mux.Handle(
			"GET /orders/{orderID}/payment",
			WithAuthentication(authUseCases, http.HandlerFunc(paymentHandler.getByOrderID)),
		)
	}
	if config.NotificationQueries != nil {
		notificationHandler := newNotificationHandler(config.NotificationQueries)
		mux.Handle(
			"GET /orders/{orderID}/notification",
			WithAuthentication(authUseCases, http.HandlerFunc(notificationHandler.getByOrderID)),
		)
	}
	if config.NotificationWebhooks != nil && strings.TrimSpace(config.NotificationWebhookSecurity.Secret) != "" {
		webhookHandler := newNotificationWebhookHandler(
			config.NotificationWebhooks, config.NotificationWebhookSecurity, config.NotificationWebhookMetrics,
		)
		mux.HandleFunc("POST /webhooks/notifications/provider", webhookHandler.receive)
	}
	if administrativeAuth, ok := authUseCases.(AdministrativeAuth); ok &&
		config.Authorization != nil && config.Audit != nil {
		handler := auditHandler{queries: config.Audit}
		mux.Handle("GET /admin/audit-events", WithPermission(
			administrativeAuth, config.Authorization, config.Audit,
			authorization.PermissionAuditRead,
			"audit.events.list", "audit_log", "audit_events",
			http.HandlerFunc(handler.list),
		))
	}

	// 返回原始路由器。
	//
	// Request ID、访问日志等横切能力由应用组装层统一包装，
	// 这样中间件顺序会在一个地方清晰可见。
	return withMatchedRoute(mux)
}

// healthHandler 只表示当前进程是否存活并能处理 HTTP 请求。
//
// 它不检查数据库或 Redis；外部依赖的可用性由 /readyz 判断。
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	// 这个响应只包含可编码的字符串，编码失败通常只可能来自
	// 客户端断开等写入问题。后面加入访问日志时再统一记录写入错误。
	_ = writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// writeJSON 统一设置 JSON 响应头、状态码并编码响应体。
//
// 将协议细节集中在辅助函数里，可以避免每个 Handler 都重复设置
// Content-Type 和调用 json.Encoder。
func writeJSON(w http.ResponseWriter, statusCode int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	return json.NewEncoder(w).Encode(value)
}
