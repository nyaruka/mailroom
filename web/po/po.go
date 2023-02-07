package flow

import (
	"context"
	"net/http"

	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/translation"
	"github.com/nyaruka/goflow/utils/i18n"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"

	"github.com/go-chi/chi/middleware"
	"github.com/pkg/errors"
)

var excludeProperties = []string{"arguments"}

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/po/export", handleExport)
	web.RegisterJSONRoute(http.MethodPost, "/mr/po/import", handleImport)
}

// Exports a PO file from the given set of flows.
//
//	{
//	  "org_id": 123,
//	  "flow_ids": [123, 354, 456],
//	  "language": "spa"
//	}
type exportRequest struct {
	OrgID    models.OrgID    `json:"org_id"  validate:"required"`
	FlowIDs  []models.FlowID `json:"flow_ids" validate:"required"`
	Language envs.Language   `json:"language" validate:"omitempty,language"`
}

func handleExport(ctx context.Context, rt *runtime.Runtime, r *http.Request, rawW http.ResponseWriter) error {
	request := &exportRequest{}
	if err := web.ReadAndValidateJSON(r, request); err != nil {
		return errors.Wrapf(err, "request failed validation")
	}

	flows, err := loadFlows(ctx, rt, request.OrgID, request.FlowIDs)
	if err != nil {
		return err
	}

	// extract everything the engine considers localizable except router arguments
	po, err := translation.ExtractFromFlows("Generated by mailroom", request.Language, excludeProperties, flows...)
	if err != nil {
		return errors.Wrapf(err, "unable to extract PO from flows")
	}

	w := middleware.NewWrapResponseWriter(rawW, r.ProtoMajor)
	w.Header().Set("Content-type", "text/x-gettext-translation")
	w.WriteHeader(http.StatusOK)
	po.Write(w)
	return nil
}

// Imports translations from a PO file into the given set of flows.
//
//	{
//	  "org_id": 123,
//	  "flow_ids": [123, 354, 456],
//	  "language": "spa"
//	}
type importForm struct {
	OrgID    models.OrgID    `form:"org_id"  validate:"required"`
	FlowIDs  []models.FlowID `form:"flow_ids" validate:"required"`
	Language envs.Language   `form:"language" validate:"required"`
}

func handleImport(ctx context.Context, rt *runtime.Runtime, r *http.Request) (interface{}, int, error) {
	form := &importForm{}
	if err := web.DecodeAndValidateForm(form, r); err != nil {
		return err, http.StatusBadRequest, nil
	}

	poFile, _, err := r.FormFile("po")
	if err != nil {
		return errors.Wrapf(err, "missing po file on request"), http.StatusBadRequest, nil
	}

	po, err := i18n.ReadPO(poFile)
	if err != nil {
		return errors.Wrapf(err, "invalid po file"), http.StatusBadRequest, nil
	}

	flows, err := loadFlows(ctx, rt, form.OrgID, form.FlowIDs)
	if err != nil {
		return err, http.StatusBadRequest, nil
	}

	err = translation.ImportIntoFlows(po, form.Language, excludeProperties, flows...)
	if err != nil {
		return err, http.StatusBadRequest, nil
	}

	return map[string]interface{}{"flows": flows}, http.StatusOK, nil
}

func loadFlows(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, flowIDs []models.FlowID) ([]flows.Flow, error) {
	// grab our org assets
	oa, err := models.GetOrgAssets(ctx, rt, orgID)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to load org assets")
	}

	flows := make([]flows.Flow, len(flowIDs))
	for i, flowID := range flowIDs {
		dbFlow, err := oa.FlowByID(flowID)
		if err != nil {
			return nil, errors.Wrapf(err, "unable to load flow with ID %d", flowID)
		}

		flow, err := oa.SessionAssets().Flows().Get(dbFlow.UUID())
		if err != nil {
			return nil, errors.Wrapf(err, "unable to read flow with UUID %s", string(dbFlow.UUID()))
		}

		flows[i] = flow
	}
	return flows, nil
}
