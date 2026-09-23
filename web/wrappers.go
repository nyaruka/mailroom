package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nyaruka/mailroom/v26/runtime"
)

type JSONHandler[T any] func(context.Context, *runtime.Runtime, *T) (any, int, error)

func JSONPayload[T any](handler JSONHandler[T]) Handler {
	return MarshaledResponse(func(ctx context.Context, rt *runtime.Runtime, r *http.Request) (any, int, error) {
		payload := new(T)

		if err := ReadAndValidateJSON(r, payload); err != nil {
			return fmt.Errorf("request failed validation: %w", err), http.StatusBadRequest, nil
		}

		return handler(ctx, rt, payload)
	})
}

// WriteDeadline wraps a handler to give it its own write deadline in place of the server's write timeout, so that a
// route which waits on an external service can be given longer without the server-wide timeout being raised for
// everything else. A handler which outlives its deadline has its connection closed without any response being
// written, so the wrapped handler should still enforce a shorter deadline on whatever it waits on and report a
// timeout as a proper response.
func WriteDeadline(d time.Duration, handler Handler) Handler {
	return func(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
		err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))

		// test recorders don't support deadlines and don't need them
		if err != nil && !errors.Is(err, http.ErrNotSupported) {
			return fmt.Errorf("error setting write deadline: %w", err)
		}

		return handler(ctx, rt, r, w)
	}
}

type MarshaledHandler func(context.Context, *runtime.Runtime, *http.Request) (any, int, error)

// MarshaledResponse wraps a handler to change the signature so that the return value is marshaled as the response
func MarshaledResponse(handler MarshaledHandler) Handler {
	return func(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
		value, status, err := handler(ctx, rt, r)
		if err != nil {
			return err
		}

		// TODO rework remaining places that handlers return error as the value
		asError, isError := value.(error)
		if isError {
			value = &ErrorResponse{Error: asError.Error()}
		}

		return WriteMarshalled(w, status, value)
	}
}

// wraps a handler to require that our request to have our global authorization header
func requireAuthToken(handler Handler) Handler {
	return func(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
		auth := r.Header.Get("authorization")

		// only do check if auth token set (might not be for dev environments)
		if rt.Config.AuthToken != "" {
			if !strings.HasPrefix(auth, "Token ") || subtle.ConstantTimeCompare([]byte(auth[6:]), []byte(rt.Config.AuthToken)) != 1 {
				return WriteMarshalled(w, http.StatusUnauthorized, &ErrorResponse{Error: "invalid or missing authorization header"})
			}
		}

		// we are authenticated, call our chain
		return handler(ctx, rt, r, w)
	}
}
