package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type userDTO struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

type userCreateRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	IsAdmin  *bool  `json:"is_admin"`
}

type meUpdateRequest struct {
	CurrentPassword string `json:"current_password"`
	Password        string `json:"password"`
}

type apiKeyDTO struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type apiKeyCreateRequest struct {
	Name string `json:"name"`
}

type apiKeyCreateResponse struct {
	apiKeyDTO
	Token string `json:"token"`
}

type systemInfoDTO struct {
	Version    string `json:"version"`
	GoVersion  string `json:"go_version"`
	BuildDate  string `json:"build_date"`
	Arch       string `json:"arch"`
	OS         string `json:"os"`
	UsersCount int    `json:"users_count"`
}

func toUserDTO(u storage.User) userDTO {
	return userDTO{
		ID:        u.ID,
		Username:  u.Username,
		IsAdmin:   u.IsAdmin,
		CreatedAt: u.CreatedAt,
	}
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	if s.users == nil {
		writeError(w, http.StatusServiceUnavailable, "user storage is not configured")
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	users, total, err := s.users.ListUsers(r.Context(), limit, offset)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list users failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, toUserDTO(u))
	}
	writeJSON(w, http.StatusOK, listResponse[[]userDTO]{Data: out, Total: total})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		writeError(w, http.StatusServiceUnavailable, "user storage is not configured")
		return
	}
	var req userCreateRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	username := strings.TrimSpace(req.Username)
	password := strings.TrimSpace(req.Password)
	if username == "" || password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	isAdmin := false
	if req.IsAdmin != nil {
		isAdmin = *req.IsAdmin
	}

	ctx := r.Context()
	count, err := s.users.CountLoginCapableUsers(ctx)
	if err != nil {
		s.log.ErrorContext(r.Context(), "count users failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// The env-provided bootstrap password is the operator's own choice and is
	// not subject to the policy; every other new password is.
	envBootstrap := false
	if p, authed := principalFromRequest(r); authed && p.UserID > 0 {
		// Authenticated caller (session, API key or AUTH_TOKEN): admin only,
		// is_admin is honoured as requested.
		if _, ok := requireAdmin(w, r); !ok {
			return
		}
	} else if count == 0 {
		// Unauthenticated first bootstrap: env admin credentials or explicit is_admin.
		if s.adminUsername != "" && s.adminPassword != "" {
			if username != s.adminUsername || password != s.adminPassword {
				writeError(w, http.StatusBadRequest, "bootstrap requires ADMIN_USERNAME/PASSWORD credentials")
				return
			}
			isAdmin = true
			envBootstrap = true
		} else if !isAdmin {
			isAdmin = true // first user is admin
		}
	} else {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if !envBootstrap {
		if err := auth.ValidateNewPassword(password); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.users.CreateUser(ctx, storage.CreateUserParams{
		Username:      username,
		PasswordHash:  hash,
		PlainPassword: password,
		IsAdmin:       isAdmin,
	})
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateUsername) {
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
		s.log.ErrorContext(r.Context(), "create user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, listResponse[userDTO]{Data: toUserDTO(u), Total: 1})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	if s.users == nil {
		writeError(w, http.StatusServiceUnavailable, "user storage is not configured")
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if id == p.UserID {
		writeError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	if err := s.users.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if errors.Is(err, storage.ErrLastAdmin) {
			writeError(w, http.StatusConflict, "cannot delete the last admin")
			return
		}
		s.log.ErrorContext(r.Context(), "delete user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.users == nil {
		return
	}
	u, err := s.users.GetUser(r.Context(), p.UserID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		s.log.ErrorContext(r.Context(), "get me failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[userDTO]{Data: toUserDTO(u), Total: 1})
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.users == nil {
		return
	}
	var req meUpdateRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	password := strings.TrimSpace(req.Password)
	if password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	cur, err := s.users.GetUser(r.Context(), p.UserID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		s.log.ErrorContext(r.Context(), "update me: get user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if cur.PasswordHash != "" && !auth.CheckPassword(cur.PasswordHash, req.CurrentPassword) {
		writeError(w, http.StatusForbidden, "current_password is incorrect")
		return
	}
	if err := auth.ValidateNewPassword(password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.users.UpdateUser(r.Context(), storage.UpdateUserParams{
		ID:            p.UserID,
		PasswordHash:  &hash,
		PlainPassword: password,
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		s.log.ErrorContext(r.Context(), "update me failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if s.sessions != nil {
		if err := s.sessions.DeleteUserSessionsExcept(r.Context(), p.UserID, auth.SessionIDFromRequest(r)); err != nil {
			s.log.WarnContext(r.Context(), "revoke sessions after password change failed", "user_id", p.UserID, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, listResponse[userDTO]{Data: toUserDTO(u), Total: 1})
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.users == nil {
		return
	}
	keys, err := s.users.ListAPIKeys(r.Context(), p.UserID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list api keys failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]apiKeyDTO, 0, len(keys))
	for _, k := range keys {
		out = append(out, apiKeyDTO{
			ID:         k.ID,
			Name:       k.Name,
			LastUsedAt: k.LastUsedAt,
			CreatedAt:  k.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, listResponse[[]apiKeyDTO]{Data: out, Total: len(out)})
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.users == nil {
		return
	}
	var req apiKeyCreateRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "default"
	}
	raw, hash, err := auth.NewAPIToken()
	if err != nil {
		s.log.ErrorContext(r.Context(), "generate api key failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	k, err := s.users.CreateAPIKey(r.Context(), storage.CreateAPIKeyParams{
		UserID:    p.UserID,
		Name:      name,
		TokenHash: hash,
	})
	if err != nil {
		s.log.ErrorContext(r.Context(), "create api key failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, listResponse[apiKeyCreateResponse]{
		Data: apiKeyCreateResponse{
			apiKeyDTO: apiKeyDTO{
				ID:        k.ID,
				Name:      k.Name,
				CreatedAt: k.CreatedAt,
			},
			Token: raw,
		},
		Total: 1,
	})
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.users == nil {
		return
	}
	keyID, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.users.DeleteAPIKey(r.Context(), p.UserID, keyID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "api key not found")
			return
		}
		s.log.ErrorContext(r.Context(), "delete api key failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	// Build/runtime details and the user count are operator information;
	// keep them away from ordinary accounts.
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	usersCount := 0
	if s.users != nil {
		if n, err := s.users.CountUsers(r.Context()); err == nil {
			usersCount = n
		}
	}
	info := systemInfoFromBuild(usersCount)
	writeJSON(w, http.StatusOK, listResponse[systemInfoDTO]{Data: info, Total: 1})
}
