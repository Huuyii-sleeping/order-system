package contract_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"example.com/order-system/api/openapi"
	authapp "example.com/order-system/internal/application/auth"
	notificationapp "example.com/order-system/internal/application/notification"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/identity"
	domainnotification "example.com/order-system/internal/domain/notification"
	"example.com/order-system/internal/domain/order"
	"example.com/order-system/internal/domain/payment"
	"example.com/order-system/internal/transport/httpapi"
)

type httpAPIDocument struct {
	OpenAPI    string                  `json:"openapi"`
	Paths      map[string]httpPathItem `json:"paths"`
	Components struct {
		Schemas         map[string]httpSchema      `json:"schemas"`
		SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
	} `json:"components"`
}

type httpPathItem map[string]httpOperation

type httpOperation struct {
	OperationID string                     `json:"operationId"`
	Responses   map[string]json.RawMessage `json:"responses"`
	Security    []map[string][]string      `json:"security"`
}

type httpSchema struct {
	Required             []string                   `json:"required"`
	Properties           map[string]json.RawMessage `json:"properties"`
	AdditionalProperties bool                       `json:"additionalProperties"`
}

func TestHTTPAPIDocumentOperationsAndSecurity(t *testing.T) {
	document := readHTTPAPIDocument(t)
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q, want 3.1.0", document.OpenAPI)
	}

	want := map[string][]string{
		"/openapi.json":                    {http.MethodGet},
		"/healthz":                         {http.MethodGet},
		"/readyz":                          {http.MethodGet},
		"/products":                        {http.MethodGet},
		"/products/{productID}":            {http.MethodGet},
		"/auth/register":                   {http.MethodPost},
		"/auth/login":                      {http.MethodPost},
		"/auth/refresh":                    {http.MethodPost},
		"/auth/logout":                     {http.MethodPost},
		"/auth/logout-all":                 {http.MethodPost},
		"/orders":                          {http.MethodGet, http.MethodPost},
		"/orders/{orderID}":                {http.MethodGet},
		"/orders/{orderID}/payment":        {http.MethodGet},
		"/orders/{orderID}/notification":   {http.MethodGet},
		"/webhooks/notifications/provider": {http.MethodPost},
	}
	if got := documentedMethods(document); !reflect.DeepEqual(got, want) {
		t.Fatalf("documented HTTP operations changed:\n got %#v\nwant %#v", got, want)
	}

	publicOperations := map[string]bool{
		"getOpenAPISpec": true,
		"getHealth":      true,
		"getReadiness":   true,
		"listProducts":   true,
		"getProduct":     true,
		"registerUser":   true,
		"login":          true,
	}
	wantResponses := map[string][]string{
		"getOpenAPISpec":             {"200"},
		"getHealth":                  {"200"},
		"getReadiness":               {"200", "503"},
		"listProducts":               {"200", "500"},
		"getProduct":                 {"200", "404", "500"},
		"registerUser":               {"201", "400", "409", "415", "500"},
		"login":                      {"200", "400", "401", "415", "429", "500", "503"},
		"refreshAccessToken":         {"200", "401", "403", "429", "500", "503"},
		"logout":                     {"204", "401", "500"},
		"logoutAll":                  {"204", "401", "500"},
		"listOrders":                 {"200", "400", "401", "500"},
		"createOrder":                {"200", "201", "400", "401", "404", "409", "415", "500"},
		"getOrder":                   {"200", "401", "404", "500"},
		"getOrderPayment":            {"200", "401", "404", "500"},
		"getOrderNotification":       {"200", "401", "404", "500"},
		"receiveNotificationWebhook": {"200", "400", "401", "404", "409", "415", "500"},
	}
	seenOperationIDs := make(map[string]string)
	for path, pathItem := range document.Paths {
		for method, operation := range pathItem {
			location := strings.ToUpper(method) + " " + path
			if operation.OperationID == "" {
				t.Errorf("%s has no operationId", location)
			}
			if previous, exists := seenOperationIDs[operation.OperationID]; exists {
				t.Errorf("operationId %q is shared by %s and %s", operation.OperationID, previous, location)
			}
			seenOperationIDs[operation.OperationID] = location
			if len(operation.Responses) == 0 {
				t.Errorf("%s has no documented responses", location)
			}
			if got, want := sortedRawKeys(operation.Responses), wantResponses[operation.OperationID]; !reflect.DeepEqual(got, want) {
				t.Errorf("%s response statuses = %v, want %v", location, got, want)
			}
			if !publicOperations[operation.OperationID] && len(operation.Security) == 0 {
				t.Errorf("%s must declare its authentication or signature boundary", location)
			}
			for _, requirement := range operation.Security {
				for scheme := range requirement {
					if _, found := document.Components.SecuritySchemes[scheme]; !found {
						t.Errorf("%s references unknown security scheme %q", location, scheme)
					}
				}
			}
		}
	}
}

func TestHTTPAPILocalReferencesResolve(t *testing.T) {
	var root any
	if err := json.Unmarshal(openapi.Document(), &root); err != nil {
		t.Fatalf("decode OpenAPI contract: %v", err)
	}
	walkJSON(t, root, root, "$")
}

func TestHTTPAPIPublicJSONSchemas(t *testing.T) {
	schemas := readHTTPAPIDocument(t).Components.Schemas
	assertSchemaFields(t, schemas, "ErrorResponse", "code", "error")
	assertSchemaFields(t, schemas, "User", "created_at", "email", "id")
	assertSchemaFields(t, schemas, "LoginResponse", "access_token", "expires_at", "token_type", "user")
	assertSchemaFields(t, schemas, "RefreshResponse", "access_token", "expires_at", "token_type")
	assertSchemaFields(t, schemas, "Product", "id", "name", "price_cents")
	assertSchemaFields(t, schemas, "ProductList", "items")
	assertSchemaFields(t, schemas, "CreateOrderRequest", "product_id", "quantity")
	assertSchemaFields(t, schemas, "Order", "created_at", "customer_id", "id", "product_id", "quantity", "status", "total_cents", "unit_price_cents")
	assertSchemaFields(t, schemas, "OrderList", "items", "next_cursor")
	assertSchemaFields(t, schemas, "Payment", "amount_cents", "created_at", "id", "order_id", "status", "updated_at")
	assertSchemaFields(t, schemas, "Notification", "created_at", "event_type", "id", "order_id", "status", "updated_at")

	for schemaName, forbidden := range map[string][]string{
		"LoginResponse": {"refresh_token", "password"},
		"Payment":       {"customer_id", "provider_reference", "failure_reason", "raw_response"},
		"Notification":  {"customer_id", "recipient_id", "provider_reference", "failure_reason"},
	} {
		for _, field := range forbidden {
			if _, found := schemas[schemaName].Properties[field]; found {
				t.Errorf("schema %s exposes forbidden field %q", schemaName, field)
			}
		}
	}
}

func TestDocumentedHTTPOperationsAreRouted(t *testing.T) {
	document := readHTTPAPIDocument(t)
	router := contractRouter()
	replacer := strings.NewReplacer("{productID}", "product-1", "{orderID}", "order-1")

	for path, pathItem := range document.Paths {
		for method := range pathItem {
			request := httptest.NewRequest(strings.ToUpper(method), replacer.Replace(path), nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code == http.StatusNotFound || response.Code == http.StatusMethodNotAllowed {
				t.Errorf("documented operation %s %s is not routed; status=%d", method, path, response.Code)
			}
		}
	}
}

func TestServedOpenAPIDocumentIsTheCheckedContract(t *testing.T) {
	response := httptest.NewRecorder()
	contractRouter().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "application/vnd.oai.openapi+json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !reflect.DeepEqual(response.Body.Bytes(), openapi.Document()) {
		t.Fatal("served OpenAPI document differs from the contract checked by CI")
	}
}

func TestRuntimeErrorMatchesStableErrorContract(t *testing.T) {
	response := httptest.NewRecorder()
	contractRouter().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/orders/order-1", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if got, want := sortedKeys(body), []string{"code", "error"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("error response fields = %v, want %v", got, want)
	}
	if body["code"] != "invalid_bearer_token" || body["error"] == "" {
		t.Fatalf("unexpected error response: %#v", body)
	}
}

func readHTTPAPIDocument(t *testing.T) httpAPIDocument {
	t.Helper()
	var document httpAPIDocument
	if err := json.Unmarshal(openapi.Document(), &document); err != nil {
		t.Fatalf("decode embedded OpenAPI contract: %v", err)
	}
	return document
}

func documentedMethods(document httpAPIDocument) map[string][]string {
	result := make(map[string][]string, len(document.Paths))
	for path, pathItem := range document.Paths {
		methods := make([]string, 0, len(pathItem))
		for method := range pathItem {
			methods = append(methods, strings.ToUpper(method))
		}
		sort.Strings(methods)
		result[path] = methods
	}
	return result
}

func assertSchemaFields(t *testing.T, schemas map[string]httpSchema, name string, want ...string) {
	t.Helper()
	schema, found := schemas[name]
	if !found {
		t.Fatalf("OpenAPI schema %q is missing", name)
	}
	if schema.AdditionalProperties {
		t.Errorf("schema %s must reject undocumented properties", name)
	}
	gotProperties := make([]string, 0, len(schema.Properties))
	for field := range schema.Properties {
		gotProperties = append(gotProperties, field)
	}
	sort.Strings(gotProperties)
	sort.Strings(schema.Required)
	sort.Strings(want)
	if !reflect.DeepEqual(gotProperties, want) {
		t.Errorf("schema %s properties = %v, want %v", name, gotProperties, want)
	}
	if !reflect.DeepEqual(schema.Required, want) {
		t.Errorf("schema %s required fields = %v, want %v", name, schema.Required, want)
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedRawKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func walkJSON(t *testing.T, root, current any, location string) {
	t.Helper()
	switch value := current.(type) {
	case map[string]any:
		if reference, ok := value["$ref"].(string); ok {
			if _, err := resolveLocalReference(root, reference); err != nil {
				t.Errorf("%s has invalid reference %q: %v", location, reference, err)
			}
		}
		for key, child := range value {
			walkJSON(t, root, child, location+"."+key)
		}
	case []any:
		for index, child := range value {
			walkJSON(t, root, child, location+"["+strconv.Itoa(index)+"]")
		}
	}
}

func resolveLocalReference(root any, reference string) (any, error) {
	if !strings.HasPrefix(reference, "#/") {
		return nil, errors.New("only local JSON references are allowed")
	}
	current := root
	for _, token := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, errors.New("reference traverses a non-object value")
		}
		current, ok = object[token]
		if !ok {
			return nil, errors.New("reference target does not exist")
		}
	}
	return current, nil
}

func contractRouter() http.Handler {
	auth := httpContractAuthStub{}
	return httpapi.NewRouterWithConfig(
		httpContractCatalogStub{}, auth, httpContractOrderStub{},
		httpapi.RouterConfig{
			PaymentQueries:       httpContractPaymentStub{},
			NotificationQueries:  httpContractNotificationStub{},
			NotificationWebhooks: httpContractWebhookStub{},
			NotificationWebhookSecurity: httpapi.NotificationWebhookSecurity{
				Secret: "contract-test-secret",
			},
		},
	)
}

type httpContractCatalogStub struct{}

func (httpContractCatalogStub) ListProducts(context.Context) ([]catalog.Product, error) {
	return nil, errors.New("contract route probe")
}
func (httpContractCatalogStub) GetProduct(context.Context, string) (catalog.Product, error) {
	return catalog.Product{}, errors.New("contract route probe")
}

type httpContractAuthStub struct{}

func (httpContractAuthStub) Register(context.Context, authapp.RegisterCommand) (identity.User, error) {
	return identity.User{}, errors.New("contract route probe")
}
func (httpContractAuthStub) Login(context.Context, authapp.LoginCommand) (authapp.LoginResult, error) {
	return authapp.LoginResult{}, errors.New("contract route probe")
}
func (httpContractAuthStub) Refresh(context.Context, string) (authapp.RefreshResult, error) {
	return authapp.RefreshResult{}, errors.New("contract route probe")
}
func (httpContractAuthStub) Authenticate(context.Context, string) (string, error) {
	return "", authapp.ErrInvalidSession
}
func (httpContractAuthStub) Logout(context.Context, string) error    { return nil }
func (httpContractAuthStub) LogoutAll(context.Context, string) error { return nil }

type httpContractOrderStub struct{}

func (httpContractOrderStub) CreateOrder(context.Context, orderapp.CreateOrderCommand) (orderapp.CreateOrderResult, error) {
	return orderapp.CreateOrderResult{}, errors.New("contract route probe")
}
func (httpContractOrderStub) GetOrder(context.Context, string, string) (order.Order, error) {
	return order.Order{}, errors.New("contract route probe")
}
func (httpContractOrderStub) ListOrders(context.Context, orderapp.ListOrdersQuery) (orderapp.ListOrdersResult, error) {
	return orderapp.ListOrdersResult{}, errors.New("contract route probe")
}

type httpContractPaymentStub struct{}

func (httpContractPaymentStub) GetPayment(context.Context, string, string) (payment.Payment, error) {
	return payment.Payment{}, errors.New("contract route probe")
}

type httpContractNotificationStub struct{}

func (httpContractNotificationStub) GetNotification(context.Context, string, string) (domainnotification.Notification, error) {
	return domainnotification.Notification{}, errors.New("contract route probe")
}

type httpContractWebhookStub struct{}

func (httpContractWebhookStub) Process(context.Context, notificationapp.WebhookEvent) (notificationapp.WebhookResult, error) {
	return notificationapp.WebhookResult{}, errors.New("contract route probe")
}
