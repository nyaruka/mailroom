package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5/middleware"
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

			if r.RequestURI != "/" && r.RequestURI != "/ping" {
				slog.Info("request completed", "listener", listener, "method", r.Method, "status", ww.Status(), "elapsed", elapsed, "length", ww.BytesWritten(), "url", uri, "user_agent", r.UserAgent())
			}
		})
	}
}

// recovers from panics, logs them to sentry and returns an HTTP 500 response
func panicRecovery(listener string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if panicVal := recover(); panicVal != nil {
					debug.PrintStack()

					sentry.CurrentHub().WithScope(func(scope *sentry.Scope) {
						scope.SetTag("listener", listener)
						sentry.CurrentHub().Recover(panicVal)
					})

					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
