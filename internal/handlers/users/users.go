package users

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/artni96/GophProfile/internal/handlers"
	"github.com/artni96/GophProfile/internal/handlers/middlewares"
	"github.com/artni96/GophProfile/internal/models"
	avatarsrepo "github.com/artni96/GophProfile/internal/repository/avatars"
	"github.com/artni96/GophProfile/pkg/interfaces"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type UserAvatarHandler struct {
	Service interfaces.ServiceI
	logger  *slog.Logger
}

func NewUserAvatarHandler(logger *slog.Logger, service interfaces.ServiceI) *UserAvatarHandler {
	return &UserAvatarHandler{
		Service: service,
		logger:  logger,
	}
}

// Get godoc
//
//	@Summary		Providing last user avatar
//	@Description	returns binary data of last user avatar
//	@Tags			users
//
//	@Accept			json
//	@Produce		image/png
//	@Produce		image/jpeg
//	@Produce		image/webp
//
//	@Param			user_id	path		int		true	"user id"
//
//	@Success		200		{string}	string	"Binary image data"
//	@Failure		400
//	@Failure		404
//	@Failure		500
//	@Router			/users/{user_id}/avatar [get]
func (h *UserAvatarHandler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		h.logger.ErrorContext(r.Context(), "failed to get user id from url param")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(handlers.ErrMsg("No user id provided"))
		return
	}
	size := r.URL.Query().Get("size")
	flt := models.AvatarFilters{
		UserID: userID,
	}
	res, err := h.Service.Get(r.Context(), flt, size)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to get user avatars", "error", err)
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
	h.logger.DebugContext(r.Context(), "got user avatar", "user_id", userID, "mime_type", res.MimeType)
	w.Write(res.Binary)
}

// Delete godoc
//
//	@Summary		removes last user avatar
//	@Description	removes last user avatar
//	@Tags			users
//	@Accpet			json
//	@Produce		json
//	@Param			user_id	path	int	true	"user id"
//	@Success		204
//	@Failure		400
//	@Failure		500
//	@Router			/users/{user_id}/avatar [delete]
func (h *UserAvatarHandler) Delete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		h.logger.ErrorContext(r.Context(), "failed to get user id from url param")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(handlers.ErrMsg("No user id provided"))
		return
	}
	flt := models.AvatarFilters{
		UserID: userID,
	}
	err := h.Service.Delete(r.Context(), flt)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to delete user avatars", "error", err)
		if errors.Is(err, avatarsrepo.ErrNotOwner) {
			w.WriteHeader(http.StatusForbidden)
			w.Write(handlers.Response403)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write(handlers.Response500)
		}
		return
	}
	h.logger.DebugContext(r.Context(), "last user avatars deleted successfully", "user_id", userID)
	w.WriteHeader(http.StatusNoContent)
}

// GetList godoc
//
//	@Summary		Returns list of user avatars metadata
//	@Descriptions	Returns list of user avatars metadata using pagination params - limit, offset
//	@Tags			users
//	@Accept			json
//	@Produce		json
//	@Param			user_id	path		int	true	"user id"
//	@Param			limit	query		int	false	"limit"
//	@Param			offset	query		int	false	"offset"
//	@Success		200		{object}	models.GetUserAvatarsListResponse
//	@Failure		400
//	@Failure		500
//	@Router			/users/{user_id}/avatars [get]
func (h *UserAvatarHandler) GetList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		h.logger.ErrorContext(r.Context(), "failed to get user id from url param")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(handlers.ErrMsg("No user id provided"))
		return
	}
	strLimit := r.URL.Query().Get("limit")
	var limit uint64
	if strLimit == "" {
		limit = 10
	} else {
		convertedLimit, err := strconv.Atoi(strLimit)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "failed to convert limit param to int", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			w.Write(handlers.ErrMsg("Invalid limit value"))
			return
		}
		limit = uint64(convertedLimit)
	}

	strOffset := r.URL.Query().Get("offset")
	var offset uint64
	if strOffset == "" {
		offset = 0
	} else {
		convertedOffset, err := strconv.Atoi(strOffset)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write(handlers.ErrMsg("Invalid offset value"))
			return
		}
		offset = uint64(convertedOffset)
	}

	flt := models.AvatarFilters{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	}
	res, err := h.Service.GetMetadataList(r.Context(), flt)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to get last user avatars", "error", err, "user_id", userID)
		if errors.Is(err, avatarsrepo.ErrAvatarNotFound) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write(handlers.Response500)
			return
		}
	}
	jsonRes, err := json.Marshal(res)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to marshal response to json", "error", err, "user_id", userID)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(handlers.Response500)
		return
	}
	h.logger.DebugContext(r.Context(), "got user avatars list", "user_id", userID)
	w.WriteHeader(http.StatusOK)
	w.Write(jsonRes)
}

func UserAvatarRouter(service interfaces.ServiceI, logger *slog.Logger) chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(otelhttp.NewMiddleware("users"))
	r.Use(middleware.Logger)
	r.Use(middlewares.PanicRecoverer(logger))
	r.Use(middlewares.GzipMiddleware)

	handler := NewUserAvatarHandler(logger, service)

	r.Route("/", func(r chi.Router) {
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf("Method %s is forbidden", r.Method)))
		})
		r.Get("/{user_id}/avatar", handler.Get)
		r.Delete("/{user_id}/avatar", handler.Delete)
		r.Get("/{user_id}/avatars", handler.GetList)
	})
	return r
}
