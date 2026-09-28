package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
)

const (
	CSRFTokenHeader = "X-CSRF-Token"
	csrfCookieName  = "csrf_token"
)

// newCSRFToken 生成只用于证明请求来自当前站点页面的随机值。
// 它不是用户身份凭证，但仍然必须不可预测。
func newCSRFToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate CSRF token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// validCSRFToken 实现 Double Submit Cookie：浏览器自动携带 Cookie，
// 页面代码还必须把相同值主动放入 Header。跨站页面无法读取本站 Cookie，
// 因此只能发出 Cookie、不能构造匹配的 Header。
func validCSRFToken(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get(CSRFTokenHeader)
	if header == "" || len(header) != len(cookie.Value) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) == 1
}
