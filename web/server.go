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
	"github.com/nyaruka/mailroom/runtime"
)

const (
	// max body bytes we'll read from a incoming request
	maxRequestBytes int64 = 1048576 * 50 // 50MB
)

type Handler func(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error

type route struct {
	method  string
	pattern string
	handler Handler
}

var routes []*route

func RegisterRoute(method string, pattern string, handler Handler) {
	routes = append(routes, &route{method, pattern, handler})
}

type Server struct {
	ctx context.Context
	rt  *runtime.Runtime

	wg *sync.WaitGroup

	httpServer *http.Server
}

// NewServer creates a new web server, it will need to be started after being created
func NewServer(ctx context.Context, rt *runtime.Runtime, wg *sync.WaitGroup) *Server {
	s := &Server{ctx: ctx, rt: rt, wg: wg}

	router := chi.NewRouter()

	//  set up our middlewares
	router.Use(middleware.Compress(flate.DefaultCompression))
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(panicRecovery)
	router.Use(middleware.Timeout(60 * time.Second))
	router.Use(requestLogger)

	// wire up our main pages
	router.NotFound(handle404)
	router.MethodNotAllowed(handle405)
	router.Get("/", s.WrapHandler(handleIndex))
	router.Get("/mr/", s.WrapHandler(handleIndex))

	// and all registered routes
	for _, route := range routes {
		router.Method(route.method, route.pattern, s.WrapHandler(route.handler))
	}

	// configure our http server
	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", rt.Config.Address, rt.Config.Port),
		Handler:      router,
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
	log := slog.With("comp", "server", "address", s.rt.Config.Address, "port", s.rt.Config.Port)

	s.wg.Add(1)

	// start serving HTTP
	go func() {
		defer s.wg.Done()

		err := s.httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Error("error listening", "error", err)
		}
	}()

	log.Info("server started")
}

// Stop stops our web server
func (s *Server) Stop() {
	log := slog.With("comp", "server")

	// shut down our HTTP server
	if err := s.httpServer.Shutdown(context.Background()); err != nil {
		log.Error("error shutting down server", "error", err)
	} else {
		log.Info("server stopped")
	}
}

func handleIndex(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
	return WriteMarshalled(w, http.StatusOK, map[string]string{
		"url":       r.URL.String(),
		"component": "mailroom",
		"version":   rt.Config.Version,
	})
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
