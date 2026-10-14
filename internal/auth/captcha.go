package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	mrand "math/rand"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const captchaChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type captchaEntry struct {
	code      string
	expiresAt time.Time
}

// CaptchaStore 内存验证码存储（单实例部署，懒清理）。
type CaptchaStore struct {
	mu      sync.Mutex
	entries map[string]captchaEntry
	ttl     time.Duration
}

func NewCaptchaStore(ttl time.Duration) *CaptchaStore {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return &CaptchaStore{entries: make(map[string]captchaEntry), ttl: ttl}
}

// Generate 生成新验证码，返回 id 与 PNG(base64)。
func (c *CaptchaStore) Generate() (id, pngBase64 string, err error) {
	code := randomCode(4)
	id = randomID()
	c.mu.Lock()
	c.gcLocked()
	c.entries[id] = captchaEntry{code: code, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()

	img := renderCaptcha(code)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", "", err
	}
	return id, base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// Verify 校验并一次性消费验证码（无论对错都失效）。
func (c *CaptchaStore) Verify(id, code string) bool {
	if id == "" || code == "" {
		return false
	}
	c.mu.Lock()
	e, ok := c.entries[id]
	delete(c.entries, id)
	c.mu.Unlock()
	if !ok || time.Now().After(e.expiresAt) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(code), e.code)
}

// Code 返回验证码答案（仅供测试使用，不消费）。
func (c *CaptchaStore) Code(id string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	return e.code, ok
}

func (c *CaptchaStore) gcLocked() {
	now := time.Now()
	for k, v := range c.entries {
		if now.After(v.expiresAt) {
			delete(c.entries, k)
		}
	}
}

func randomCode(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = captchaChars[int(b[i])%len(captchaChars)]
	}
	return string(out)
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// renderCaptcha 用点阵字体绘制验证码并放大 2 倍，附加干扰线与噪点。
// 输出为 PNG，字符不会以明文出现在响应中。
func renderCaptcha(code string) *image.RGBA {
	const sw, sh = 88, 32 // 低分辨率画布
	const scale = 2

	small := image.NewRGBA(image.Rect(0, 0, sw, sh))
	draw.Draw(small, small.Bounds(), image.NewUniform(color.RGBA{R: 248, G: 249, B: 252, A: 255}), image.Point{}, draw.Src)

	for i, ch := range code {
		col := color.RGBA{
			R: uint8(30 + mrand.Intn(90)),
			G: uint8(30 + mrand.Intn(90)),
			B: uint8(60 + mrand.Intn(120)),
			A: 255,
		}
		d := &font.Drawer{
			Dst:  small,
			Src:  image.NewUniform(col),
			Face: basicfont.Face7x13,
			Dot:  fixed.P(8+i*19+mrand.Intn(4), 7+mrand.Intn(10)),
		}
		d.DrawString(string(ch))
	}

	big := image.NewRGBA(image.Rect(0, 0, sw*scale, sh*scale))
	xdraw.NearestNeighbor.Scale(big, big.Bounds(), small, small.Bounds(), draw.Src, nil)

	// 干扰线
	for i := 0; i < 6; i++ {
		col := color.RGBA{
			R: uint8(60 + mrand.Intn(120)),
			G: uint8(60 + mrand.Intn(120)),
			B: uint8(60 + mrand.Intn(120)),
			A: 255,
		}
		drawLine(big, mrand.Intn(big.Bounds().Dx()), mrand.Intn(big.Bounds().Dy()),
			mrand.Intn(big.Bounds().Dx()), mrand.Intn(big.Bounds().Dy()), col)
	}
	// 噪点
	for i := 0; i < 260; i++ {
		col := color.RGBA{
			R: uint8(mrand.Intn(200)),
			G: uint8(mrand.Intn(200)),
			B: uint8(mrand.Intn(200)),
			A: 255,
		}
		big.Set(mrand.Intn(big.Bounds().Dx()), mrand.Intn(big.Bounds().Dy()), col)
	}
	return big
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, col color.Color) {
	steps := int(math.Max(math.Abs(float64(x1-x0)), math.Abs(float64(y1-y0))))
	if steps == 0 {
		img.Set(x0, y0, col)
		return
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		img.Set(x0+int(float64(x1-x0)*t), y0+int(float64(y1-y0)*t), col)
	}
}
