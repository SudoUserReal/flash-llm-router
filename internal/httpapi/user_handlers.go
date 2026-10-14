package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"flash-llm-router/internal/auth"
	"flash-llm-router/internal/store"
)

func validRole(r string) (store.Role, bool) {
	switch store.Role(r) {
	case store.RoleAdmin, store.RoleOperator, store.RoleViewer:
		return store.Role(r), true
	}
	return "", false
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, _ *store.User) {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

type createUserReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, _ *store.User) {
	var req createUserReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 2 || len(req.Username) > 32 {
		errJSON(w, http.StatusBadRequest, "用户名长度需在 2-32 之间")
		return
	}
	role, ok := validRole(req.Role)
	if !ok {
		errJSON(w, http.StatusBadRequest, "角色必须是 admin / operator / viewer")
		return
	}
	if err := auth.ValidatePasswordPolicy(req.Password); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "密码处理失败")
		return
	}
	u, err := s.Store.CreateUser(r.Context(), req.Username, hash, role)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			errJSON(w, http.StatusConflict, "用户名已存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

type updateUserReq struct {
	Role     *string `json:"role"`
	Enabled  *bool   `json:"enabled"`
	Password *string `json:"password"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request, actor *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	var req updateUserReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	var roleVal *store.Role
	if req.Role != nil {
		role, ok := validRole(*req.Role)
		if !ok {
			errJSON(w, http.StatusBadRequest, "角色必须是 admin / operator / viewer")
			return
		}
		roleVal = &role
	}
	if id == actor.ID {
		if roleVal != nil && *roleVal != store.RoleAdmin {
			errJSON(w, http.StatusBadRequest, "不能降低自己的角色")
			return
		}
		if req.Enabled != nil && !*req.Enabled {
			errJSON(w, http.StatusBadRequest, "不能禁用自己")
			return
		}
	}
	// 防止移除最后一个可用 admin
	if (roleVal != nil && *roleVal != store.RoleAdmin) || (req.Enabled != nil && !*req.Enabled) {
		target, err := s.Store.GetUser(r.Context(), id)
		if err != nil {
			errJSON(w, http.StatusNotFound, "用户不存在")
			return
		}
		if target.Role == store.RoleAdmin {
			users, _ := s.Store.ListUsers(r.Context())
			admins := 0
			for _, u := range users {
				if u.Role == store.RoleAdmin && u.Enabled && u.ID != id {
					admins++
				}
			}
			if admins == 0 {
				errJSON(w, http.StatusBadRequest, "系统至少需要保留一个可用管理员")
				return
			}
		}
	}

	var hashPtr *string
	if req.Password != nil && *req.Password != "" {
		if err := auth.ValidatePasswordPolicy(*req.Password); err != nil {
			errJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		h, err := auth.HashPassword(*req.Password)
		if err != nil {
			errJSON(w, http.StatusInternalServerError, "密码处理失败")
			return
		}
		hashPtr = &h
	}

	if err := s.Store.UpdateUser(r.Context(), id, roleVal, req.Enabled, hashPtr); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "用户不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 禁用或改密后强制下线
	if hashPtr != nil || (req.Enabled != nil && !*req.Enabled) {
		_ = s.Store.DeleteSessionsForUser(r.Context(), id)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, actor *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	if id == actor.ID {
		errJSON(w, http.StatusBadRequest, "不能删除自己")
		return
	}
	target, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		errJSON(w, http.StatusNotFound, "用户不存在")
		return
	}
	if target.Role == store.RoleAdmin {
		users, _ := s.Store.ListUsers(r.Context())
		admins := 0
		for _, u := range users {
			if u.Role == store.RoleAdmin && u.Enabled && u.ID != id {
				admins++
			}
		}
		if admins == 0 {
			errJSON(w, http.StatusBadRequest, "系统至少需要保留一个可用管理员")
			return
		}
	}
	if err := s.Store.DeleteUser(r.Context(), id); err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
