package web

import (
	"compress/flate"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const (
	// max body bytes we'll read from a incoming request
	maxRequestBytes int64 = 1048576 * 50 // 50MB
)

type Handler func(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error

type route struct {
	method   string
	pattern  string
	handler  Handler
	internal bool
}

var routes []*route

// PublicRoute registers a route that handles direct requests from the internet
func PublicRoute(method string, pattern string, handler Handler) {
	routes = append(routes, &route{method: method, pattern: "/mr" + pattern, handler: handler})
}

// InternalRoute registers a route that handles internal requests between components
func InternalRoute(method string, pattern string, handler Handler) {
	routes = append(routes, &route{method: method, pattern: "/mi" + pattern, handler: requireAuthToken(handler), internal: true})
}

type Server struct {
	ctx context.Context
	rt  *runtime.Runtime

	wg *sync.WaitGroup

	publicServer   *http.Server
	internalServer *http.Server
}

// NewServer creates a new web server, it will need to be started after being created
func NewServer(ctx context.Context, rt *runtime.Runtime, wg *sync.WaitGroup) *Server {
	s := &Server{ctx: ctx, rt: rt, wg: wg}

	// public listener — exposes /mr/* and (during transition) /mi/* as well
	publicRouter := chi.NewRouter()
	publicRouter.Use(middleware.Compress(flate.DefaultCompression))
	publicRouter.Use(middleware.RequestID)
	publicRouter.Use(middleware.RealIP)
	publicRouter.Use(panicRecovery("public"))
	publicRouter.Use(middleware.Timeout(60 * time.Second))
	publicRouter.Use(requestLogger("public"))
	publicRouter.NotFound(handle404)
	publicRouter.MethodNotAllowed(handle405)
	publicRouter.Get("/", s.WrapHandler(handleIndex))
	publicRouter.Get("/ping", handlePing)
	for _, route := range routes {
		publicRouter.Method(route.method, route.pattern, s.WrapHandler(route.handler))
	}

	// internal listener — only /mi/* routes, no public-facing concerns
	internalRouter := chi.NewRouter()
	internalRouter.Use(middleware.Compress(flate.DefaultCompression))
	internalRouter.Use(middleware.RequestID)
	internalRouter.Use(panicRecovery("internal"))
	internalRouter.Use(middleware.Timeout(60 * time.Second))
	internalRouter.Use(requestLogger("internal"))
	internalRouter.NotFound(func(w http.ResponseWriter, r *http.Request) {
		slog.Error("internal 404", "method", r.Method, "path", r.URL.Path)
		handle404(w, r)
	})
	internalRouter.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		slog.Error("internal 405", "method", r.Method, "path", r.URL.Path)
		handle405(w, r)
	})
	internalRouter.Get("/ping", handlePing)
	for _, route := range routes {
		if route.internal {
			internalRouter.Method(route.method, route.pattern, s.WrapHandler(route.handler))
		}
	}

	s.publicServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", rt.Config.PublicAddress, rt.Config.PublicPort),
		Handler:      publicRouter,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  90 * time.Second,
	}
	s.internalServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", rt.Config.InternalAddress, rt.Config.InternalPort),
		Handler:      internalRouter,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  90 * time.Second,
	}

	return s
}

// WrapHandler wraps a simple handler and
//  1. adds server runtime to the handler func
//  2. allows an error return value to be logged and returned as a 500
func (s *Server) WrapHandler(handler Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := handler(r.Context(), s.rt, r, w)
		if err == nil {
			return
		}

		resp, status := ErrorToResponse(err)

		if status == http.StatusInternalServerError {
			slog.Error("error handling request", "comp", "server", "request", r, "error", err)
		}

		WriteMarshalled(w, status, resp)
	}
}

// Start starts our web server, listening for new requests
func (s *Server) Start() {
	s.wg.Add(2)

	go func() {
		defer s.wg.Done()

		log := slog.With("comp", "server", "listener", "public", "address", s.publicServer.Addr)
		log.Info("server started")

		err := s.publicServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Error("error listening", "error", err)
		}
	}()

	go func() {
		defer s.wg.Done()

		log := slog.With("comp", "server", "listener", "internal", "address", s.internalServer.Addr)
		log.Info("server started")

		err := s.internalServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Error("error listening", "error", err)
		}
	}()
}

// Stop stops our web server
func (s *Server) Stop() {
	if err := s.publicServer.Shutdown(context.Background()); err != nil {
		slog.Error("error shutting down server", "comp", "server", "listener", "public", "error", err)
	} else {
		slog.Info("server stopped", "comp", "server", "listener", "public")
	}

	if err := s.internalServer.Shutdown(context.Background()); err != nil {
		slog.Error("error shutting down server", "comp", "server", "listener", "internal", "error", err)
	} else {
		slog.Info("server stopped", "comp", "server", "listener", "internal")
	}
}

func handleIndex(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
	return WriteMarshalled(w, http.StatusOK, map[string]string{
		"url":       r.URL.String(),
		"component": "mailroom",
		"version":   rt.Config.Version,
	})
}

// handlePing is a lightweight liveness probe used by ALB health checks. Registered at the
// root of both listeners and not under any /mr or /mi prefix, so no ALB listener rule routes
// client traffic to it — only direct ALB→target health probes reach it.
func handlePing(w http.ResponseWriter, r *http.Request) {
	WriteMarshalled(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handle404(w http.ResponseWriter, r *http.Request) {
	WriteMarshalled(w, http.StatusNotFound, &ErrorResponse{Error: fmt.Sprintf("not found: %s", r.URL.String())})
}

func handle405(w http.ResponseWriter, r *http.Request) {
	WriteMarshalled(w, http.StatusMethodNotAllowed, &ErrorResponse{Error: fmt.Sprintf("illegal method: %s", r.Method)})
}

func WriteMarshalled(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-type", "application/json")
	w.WriteHeader(status)

	marshaled, err := jsonx.MarshalPretty(value)
	if err != nil {
		return err
	}

	w.Write(marshaled)
	return nil
}
