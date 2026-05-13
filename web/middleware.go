package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5/middleware"
)

func requestLogger(next http.Handler) http.Handler {
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
			slog.Info("request completed", "method", r.Method, "status", ww.Status(), "elapsed", elapsed, "length", ww.BytesWritten(), "url", uri, "user_agent", r.UserAgent())

		}
	})
}

func logPlaintextWebhook(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/mr/") && r.Header.Get("X-Forwarded-Proto") == "http" {
			slog.Warn("plaintext webhook",
				"path", r.URL.Path,
				"method", r.Method,
				"ua", r.UserAgent(),
				"from", r.Header.Get("X-Forwarded-For"),
			)
		}
		next.ServeHTTP(w, r)
	})
}

// recovers from panics, logs them to sentry and returns an HTTP 500 response
func panicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if panicVal := recover(); panicVal != nil {
				debug.PrintStack()

				sentry.CurrentHub().Recover(panicVal)

				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
