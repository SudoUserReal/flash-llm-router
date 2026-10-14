package auth

import (
	"bytes"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCaptchaLifecycle(t *testing.T) {
	cs := NewCaptchaStore(time.Minute)

	id, img, err := cs.Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if id == "" || img == "" {
		t.Fatal("empty captcha output")
	}
	raw, err := base64.StdEncoding.DecodeString(img)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("captcha image is not a PNG")
	}

	code, ok := cs.Code(id)
	if !ok || len(code) != 4 {
		t.Fatalf("code not found or wrong length: %q", code)
	}

	// 错误验证码：失败并一次性失效
	if cs.Verify(id, "!!!!") {
		t.Fatal("wrong code should not verify")
	}
	if cs.Verify(id, code) {
		t.Fatal("captcha must be single-use")
	}

	// 正确验证码（含大小写不敏感）
	id2, _, _ := cs.Generate()
	code2, _ := cs.Code(id2)
	if !cs.Verify(id2, strings.ToLower(code2)) {
		t.Fatal("correct lowercase code should verify")
	}
}

func TestCaptchaExpiry(t *testing.T) {
	cs := NewCaptchaStore(time.Millisecond)
	id, _, _ := cs.Generate()
	code, _ := cs.Code(id)
	time.Sleep(5 * time.Millisecond)
	if cs.Verify(id, code) {
		t.Fatal("expired captcha should not verify")
	}
}

func TestPasswordPolicy(t *testing.T) {
	cases := []struct {
		pw string
		ok bool
	}{
		{"short1", false},
		{"alllowercase", false},
		{"12345678", false},
		{"goodpass123", true},
		{"TestAccess123", true},
	}
	for _, c := range cases {
		if err := ValidatePasswordPolicy(c.pw); (err == nil) != c.ok {
			t.Errorf("policy(%q) = %v, want ok=%v", c.pw, err, c.ok)
		}
	}
}

func TestHashAndToken(t *testing.T) {
	h, err := HashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassword(h, "secret123"); err != nil {
		t.Fatal("verify failed for correct password")
	}
	if err := VerifyPassword(h, "wrong"); err == nil {
		t.Fatal("verify should fail for wrong password")
	}

	plain, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if plain == hash || hash == "" {
		t.Fatal("token must not equal its hash")
	}
	if HashToken(plain) != hash {
		t.Fatal("hash mismatch")
	}
}

// TestCaptchaPreview 在 CAPTCHA_DUMP 环境变量下输出一张样例图片，便于人工检查观感。
func TestCaptchaPreview(t *testing.T) {
	path := os.Getenv("CAPTCHA_DUMP")
	if path == "" {
		t.Skip("set CAPTCHA_DUMP=/tmp/captcha.png to dump a sample")
	}
	cs := NewCaptchaStore(time.Minute)
	id, img, err := cs.Generate()
	if err != nil {
		t.Fatal(err)
	}
	code, _ := cs.Code(id)
	raw, _ := base64.StdEncoding.DecodeString(img)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("captcha code=%s saved to %s", code, path)
}
