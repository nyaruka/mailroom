package system

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/system/errors", web.JSONPayload(handleErrors))
}

type errorsRequest struct {
	Log    string
	Return string
	Panic  string
}

func handleErrors(ctx context.Context, rt *runtime.Runtime, r *errorsRequest) (any, int, error) {
	if r.Log != "" {
		slog.Error(r.Log)
	}
	if r.Return != "" {
		return nil, http.StatusInternalServerError, errors.New(r.Return)
	}
	if r.Panic != "" {
		panic(r.Panic)
	}

	return map[string]any{}, http.StatusOK, nil
}
