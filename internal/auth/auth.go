package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

var ErrBadPassword = errors.New("invalid password")

func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func VerifyPassword(hash, plain string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		return ErrBadPassword
	}
	return nil
}

// NewSessionToken 生成会话 token 与其 sha256 哈希（数据库只存哈希）。
func NewSessionToken() (plain, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plain = "sess-" + hex.EncodeToString(b)
	return plain, HashToken(plain), nil
}

func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// ValidatePasswordPolicy 密码至少 8 位，且包含字母与数字。
func ValidatePasswordPolicy(pw string) error {
	if len(pw) < 8 {
		return fmt.Errorf("密码至少 8 位")
	}
	hasLetter, hasDigit := false, false
	for _, r := range pw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("密码需同时包含字母和数字")
	}
	return nil
}
