package ivr

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"time"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

type ResumeType string

const (
	InputResumeType   = ResumeType("input")
	DialResumeType    = ResumeType("dial")
	TimeoutResumeType = ResumeType("timeout")
)

// Resume is our interface for a type of IVR resume
type Resume interface {
	Type() ResumeType
}

// InputResume is our type for resumes as consequences of user inputs (either a digit or recording)
type InputResume struct {
	Input      string
	Attachment utils.Attachment
}

// Type returns the type for InputResume
func (r InputResume) Type() ResumeType {
	return InputResumeType
}

// DialResume is our type for resumes as consequences of dials/transfers completing
type DialResume struct {
	Status   flows.DialStatus
	Duration int
}

// Type returns the type for DialResume
func (r DialResume) Type() ResumeType {
	return DialResumeType
}

func buildDialResume(resume DialResume) (flows.Resume, error, error) {
	return resumes.NewDial(events.NewDialEnded(flows.NewDial(resume.Status, resume.Duration))), nil, nil
}

func buildMsgResume(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, svc Service, channel *models.Channel, urn urns.URN, call *models.Call, flow *models.Flow, resume InputResume) (*models.MsgInRef, flows.Resume, error, error) {
	// our msg UUID
	msgUUID := flows.NewEventUUID()

	// we have an attachment, download it locally
	if resume.Attachment != NilAttachment {
		var err error
		var resp *http.Response
		for retry := 0; retry < 45; retry++ {
			resp, err = svc.DownloadMedia(resume.Attachment.URL())
			if err == nil && resp.StatusCode == 200 {
				break
			}
			time.Sleep(time.Second)

			if resp != nil {
				slog.Info("retrying download of attachment", "retry", retry, "status", resp.StatusCode, "url", resume.Attachment.URL())
			} else {
				slog.Info("retrying download of attachment", "error", err, "retry", retry, "url", resume.Attachment.URL())
			}
		}

		if err != nil {
			return nil, nil, fmt.Errorf("error downloading attachment, ending call: %w", err), nil
		}

		if resp == nil {
			return nil, nil, fmt.Errorf("unable to download attachment, ending call"), nil
		}

		// filename is based on our org id and msg UUID
		filename := string(msgUUID) + path.Ext(resume.Attachment.URL())

		resume.Attachment, err = oa.Org().StoreAttachment(ctx, rt, filename, resume.Attachment.ContentType(), resp.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to download and store attachment, ending call: %w", err), nil
		}
	}

	attachments := []utils.Attachment{}
	if resume.Attachment != NilAttachment {
		attachments = []utils.Attachment{resume.Attachment}
	}

	msgIn := flows.NewMsgIn(urn, channel.Reference(), resume.Input, attachments, "")
	msgEvt := events.NewMsgReceived(msgIn)
	msgEvt.UUID_ = msgUUID

	// we currently model timeouts as empty messages.. if we have one of those, don't save it
	if resume.Input == "" && len(attachments) == 0 {
		return nil, resumes.NewMsg(msgEvt), nil, nil
	}

	msg := models.NewIncomingIVR(rt.Config, oa.OrgID(), call, flow, msgEvt)
	if err := models.InsertMessages(ctx, rt.DB, []*models.Msg{msg}); err != nil {
		return nil, nil, nil, fmt.Errorf("error committing new message: %w", err)
	}

	// create our msg resume event
	return &models.MsgInRef{UUID: msg.UUID(), Handled: true}, resumes.NewMsg(msgEvt), nil, nil
}
