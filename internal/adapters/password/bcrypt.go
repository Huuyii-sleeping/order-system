// Package password 提供密码哈希适配器。
package password

import "golang.org/x/crypto/bcrypt"

type Bcrypt struct {
	cost int
}

func NewBcrypt(cost int) Bcrypt {
	return Bcrypt{cost: cost}
}

func (b Bcrypt) Hash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), b.cost)
	return string(hash), err
}

func (b Bcrypt) Matches(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
