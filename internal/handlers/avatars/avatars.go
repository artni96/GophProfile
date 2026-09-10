package avatars

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"uuid"

	"github.com/artni96/GophProfile/internal/handlers"
	"github.com/artni96/GophProfile/internal/handlers/middlewares"
	"github.com/artni96/GophProfile/internal/models"
	avatarsrepo "github.com/artni96/GophProfile/internal/repository/avatars"
	"github.com/artni96/GophProfile/pkg/interfaces"
	"go.uber.org/zap"
	_ "golang.org/x/image/webp"

	"github.com/artni96/GophProfile/internal/services/avatars"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type AvatarHandler struct {
	Service interfaces.ServiceI
	ctx     context.Context
}

func NewAvatarHandler(ctx context.Context, service interfaces.ServiceI) *AvatarHandler {
	return &AvatarHandler{
		Service: service,
		ctx:     ctx,
	}
}

// Upload godoc
//
//	@Summary		Uploading new user avatar
//	@Description	Uploading new user avatar with thumbnails (100x100, 300x300)
//	@Tags			avatars
//
//	@Accept			multipart/form-data
//
//	@Produce		json
//	@Param			X-User-Id	header		int		true	"user id"
//	@Param			file		formData	file	true	"file binary data"
//	@Success		201			{object}	models.SaveAvatarResponse
//	@Failure		400
//	@Failure		413
//	@Failure		500
//	@Router			/avatars [post]
func (h *AvatarHandler) Upload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write(handlers.ErrMsg("no image in form"))
		return
	}
	userID := r.Header.Get("X-User-Id")
	if userID == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write(handlers.ErrMsg("no user id in header"))
		return
	}

	res, err := h.Service.Save(h.ctx, file, header, userID)
	if err != nil {
		if errors.Is(err, avatars.ErrExceededSize) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			w.Write(handlers.FileTooLargeResponse)
		} else if errors.Is(err, avatars.ErrUnsupportedFormat) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write(handlers.UnsupportedFormatResponse)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write(handlers.Response500)
		}
		return
	}

	bytesRes, err := json.Marshal(res)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(handlers.Response500)
	}
	w.WriteHeader(http.StatusCreated)
	w.Write(bytesRes)
}

// Get godoc
//
//	@Summary		Providing user avatar by id
//	@Description	returns user avatar by id (binary)
//	@Tags			avatars
//
//	@Accept			json
//	@Produce		image/png
//	@Produce		image/jpeg
//	@Produce		image/webp
//
//	@Param			avatar_id	path		string	true	"avatar id"
//
//	@Param			size		path		string	false	"size"	Enums(original, 100x100, 300x300)
//
//	@Success		200			{string}	string	"Binary image data"
//	@Failure		404
//	@Failure		500
//	@Router			/avatars/{avatar_id} [get]
func (h *AvatarHandler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	strAvatarID := chi.URLParam(r, "avatar_id")
	avatarID, err := uuid.Parse(strAvatarID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		w.Write(handlers.ErrMsg("Avatar not found"))
		return
	}
	size := r.URL.Query().Get("size")
	flt := models.AvatarFilters{
		ID: avatarID,
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

// GetMetadata godoc
//
//	@Summary		Providing user avatar metadata by id
//	@Description	returns user avatar metadata by id
//	@Tags			avatars
//
//	@Accept			json
//	@Produce		json
//
//	@Param			avatar_id	path		string						true	"avatar id"
//
//	@Success		200			{object}	models.GetAvatarMetadata	"avatar metadata"
//	@Failure		404
//	@Failure		500
//	@Router			/avatars/{avatar_id}/metadata [get]
func (h *AvatarHandler) GetMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	avatarID := chi.URLParam(r, "avatarID")
	if avatarID == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write(handlers.ErrMsg("failed to parse avatar_id"))
		return
	}
	parsedAvatarID := uuid.MustParse(avatarID)
	res, err := h.Service.GetMetadata(h.ctx, parsedAvatarID)
	if err != nil {
		if errors.Is(err, avatarsrepo.ErrAvatarNotFound) {
			w.WriteHeader(http.StatusNotFound)
			w.Write(handlers.ErrMsg(fmt.Sprintf("avatar with id %s not found", avatarID)))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(handlers.Response500)
		return
	}
	jsonRes, err := json.Marshal(res)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(handlers.Response500)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(jsonRes)
}

// Delete godoc
//
//	@Summary		Removing user avatar by id
//	@Description	removes user avatar by id
//	@Tags			avatars
//
//	@Accept			json
//	@Produce		json
//
//	@Param			avatar_id	path	string	true	"avatar id"
//	@Param			X-User-Id	header	int		true	"user id"
//
//	@Success		204
//	@Failure		400
//	@Failure		403
//	@Failure		500
//	@Router			/avatars/{avatar_id} [delete]
func (h *AvatarHandler) Delete(w http.ResponseWriter, r *http.Request) {
	avatarID := chi.URLParam(r, "avatarID")
	if avatarID == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write(handlers.ErrMsg("failed to parse avatar_id"))
		return
	}
	userID := r.Header.Get("X-User-Id")
	if userID == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write(handlers.ErrMsg("no user id in header"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	parsedAvatarID := uuid.MustParse(avatarID)
	flt := models.AvatarFilters{
		ID:     parsedAvatarID,
		UserID: userID,
	}
	err := h.Service.Delete(h.ctx, flt)
	if err != nil {
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

func AvatarRouter(ctx context.Context, service interfaces.ServiceI, logger *zap.Logger) chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middlewares.PanicRecoverer(logger))
	r.Use(middleware.RequestID)
	r.Use(middlewares.GzipMiddleware)

	handler := NewAvatarHandler(ctx, service)

	r.Route("/", func(r chi.Router) {
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf("Method %s is forbidden", r.Method)))
		})
		r.Post("/", handler.Upload)
		r.Get("/{avatar_id}", handler.Get)
		r.Get("/{avatarID}/metadata", handler.GetMetadata)
		r.Delete("/{avatarID}", handler.Delete)
	})
	return r
}
