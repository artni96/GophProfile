package users

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/artni96/GophProfile/internal/handlers"
	"github.com/artni96/GophProfile/internal/models"
	avatarsrepo "github.com/artni96/GophProfile/internal/repository/avatars"
	"github.com/artni96/GophProfile/pkg/interfaces"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type UserAvatarHandler struct {
	Service interfaces.ServiceI
	ctx     context.Context
}

func NewUserAvatarHandler(ctx context.Context, service interfaces.ServiceI) *UserAvatarHandler {
	return &UserAvatarHandler{
		Service: service,
		ctx:     ctx,
	}
}

func (h *UserAvatarHandler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write(handlers.ErrMsg("No user id provided"))
		return
	}
	size := r.URL.Query().Get("size")
	flt := models.AvatarMetadataFilters{
		UserID: userID,
	}
	res, err := h.Service.Get(h.ctx, flt, size)
	if err != nil {
		if errors.Is(err, avatarsrepo.ErrAvatarNotFound) {
			w.WriteHeader(http.StatusNotFound)
			w.Write(handlers.ErrMsg("Avatar not found"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(handlers.Response500)
		return
	}
	w.Header().Set("Content-Type", res.MimeType)
	w.Write(res.Binary)
}

func (h *UserAvatarHandler) Delete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write(handlers.ErrMsg("No user id provided"))
		return
	}
	flt := models.AvatarMetadataFilters{
		UserID: userID,
	}
	err := h.Service.Delete(h.ctx, flt)
	if err != nil {
		if errors.Is(err, avatarsrepo.ErrAvatarNotFound) {
			w.WriteHeader(http.StatusNotFound)
			w.Write(handlers.ErrMsg("Avatar not found"))
		}
		if errors.Is(err, avatarsrepo.ErrNotOwner) {
			w.WriteHeader(http.StatusForbidden)
			w.Write(handlers.Response403)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write(handlers.Response500)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func UserAvatarRouter(ctx context.Context, service interfaces.ServiceI) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	handler := NewUserAvatarHandler(ctx, service)

	r.Route("/", func(r chi.Router) {
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf("Method %s is forbidden", r.Method)))
		})
		r.Get("/{user_id}/avatar", handler.Get)
		r.Delete("/{user_id}/avatar", handler.Delete)
	})
	return r
}
