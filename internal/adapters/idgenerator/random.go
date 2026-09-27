// Package idgenerator 提供业务实体 ID 的生成实现。
package idgenerator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Random 使用系统安全随机源生成 128 位 ID。
type Random struct{}

func (Random) NewID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return hex.EncodeToString(raw[:]), nil
}
