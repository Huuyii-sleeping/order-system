// Package sessiontoken 生成只能由客户端持有的高熵会话凭证。
package sessiontoken

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

type Random struct{}

func (Random) NewToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
