package middlewares

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/artni96/GophProfile/internal/config"
	"go.uber.org/zap"
)

func PanicRecoverer(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Info("got panic",
						zap.String("error message", fmt.Sprintf("panic recovered: %v\n", recovered)),
						zap.String("call stack", string(debug.Stack())),
					)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					resp, _ := json.Marshal(struct {
						Error string `json:"error"`
					}{
						Error: "Internal Server Error",
					})
					w.Write(resp)
					return
				}
			}()

			next.ServeHTTP(w, r)

		}
		return http.HandlerFunc(fn)
	}
}

func GzipMiddleware(h http.Handler) http.Handler {
	gzipMW := func(w http.ResponseWriter, r *http.Request) {
		ow := w

		acceptEncoding := r.Header.Get("Accept-Encoding")
		supportsGzip := strings.Contains(acceptEncoding, "gzip")
		if supportsGzip {
			cw := config.NewCompressWriter(w)
			ow = cw
			defer cw.Close()
		}

		contentEncoding := r.Header.Get("Content-Encoding")
		sendsGzip := strings.Contains(contentEncoding, "gzip")
		if sendsGzip {
			cr, err := config.NewCompressReader(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			r.Body = cr
			defer cr.Close()
		}

		h.ServeHTTP(ow, r)
	}
	return http.HandlerFunc(gzipMW)
}
