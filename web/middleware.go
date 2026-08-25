package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func requestLogger(listener string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(ww, r)

			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}

			elapsed := time.Since(start)
			uri := fmt.Sprintf("%s://%s%s", scheme, r.Host, r.RequestURI)
			ww.Header().Set("X-Elapsed-NS", strconv.FormatInt(int64(elapsed), 10))

			if r.RequestURI != "/" {
				slog.Info("request completed", "listener", listener, "method", r.Method, "status", ww.Status(), "elapsed", elapsed, "length", ww.BytesWritten(), "url", uri, "user_agent", r.UserAgent())
			}
		})
	}
}

// recovers from panics, reports them and returns an HTTP 500 response
func panicRecovery(listener string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if panicVal := recover(); panicVal != nil {
					runtime.PanicHandler(panicVal, map[string]string{"listener": listener})

					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
