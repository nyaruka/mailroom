package public

import (
	"context"
	"net/http"

	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

var docServer http.Handler

func init() {
	docServer = http.StripPrefix("/mr/docs", http.FileServer(http.Dir("docs")))

	// redirect non slashed docs to slashed version so relative URLs work
	web.PublicRoute(http.MethodGet, "/docs", addSlashRedirect)

	// all slashed docs are served by our static dir
	web.PublicRoute(http.MethodGet, "/docs/*", handleDocs)
}

func addSlashRedirect(ctx context.Context, rt *runtime.Runtime, r *http.Request, rawW http.ResponseWriter) error {
	http.Redirect(rawW, r, r.URL.Path+"/", http.StatusMovedPermanently)
	return nil
}

func handleDocs(ctx context.Context, rt *runtime.Runtime, r *http.Request, rawW http.ResponseWriter) error {
	docServer.ServeHTTP(rawW, r)
	return nil
}
