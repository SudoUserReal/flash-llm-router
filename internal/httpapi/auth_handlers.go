package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"flash-llm-router/internal/auth"
	"flash-llm-router/internal/store"
)

func (s *Server) handleCaptcha(w http.ResponseWriter, r *http.Request) {
	id, img, err := s.Captcha.Generate()
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "生成验证码失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"captcha_id": id,
		"image":      "data:image/png;base64," + img,
	})
}

type loginReq struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	CaptchaID   string `json:"captcha_id"`
	CaptchaCode string `json:"captcha_code"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)

	if !s.Captcha.Verify(req.CaptchaID, req.CaptchaCode) {
		errJSON(w, http.StatusBadRequest, "验证码错误或已过期，请刷新验证码")
		return
	}

	u, err := s.Store.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusUnauthorized, "用户名或密码错误")
			return
		}
		errJSON(w, http.StatusInternalServerError, "登录失败")
		return
	}
	if !u.Enabled {
		errJSON(w, http.StatusForbidden, "账号已被禁用")
		return
	}
	if err := auth.VerifyPassword(u.PasswordHash, req.Password); err != nil {
		errJSON(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	plain, hashToken, err := auth.NewSessionToken()
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "登录失败")
		return
	}
	expires := time.Now().Add(s.SessionTTL)
	if err := s.Store.CreateSession(r.Context(), u.ID, hashToken, expires); err != nil {
		errJSON(w, http.StatusInternalServerError, "登录失败")
		return
	}
	_ = s.Store.TouchLastLogin(r.Context(), u.ID)

	writeJSON(w, http.StatusOK, map[string]any{
		"token":      plain,
		"expires_at": expires,
		"user": map[string]any{
			"id":       u.ID,
			"username": u.Username,
			"role":     u.Role,
		},
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, u *store.User) {
	if token := bearerToken(r); token != "" {
		_ = s.Store.DeleteSession(r.Context(), auth.HashToken(token))
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, u *store.User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       u.ID,
		"username": u.Username,
		"role":     u.Role,
	})
}
