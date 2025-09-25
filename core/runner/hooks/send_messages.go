package hooks

import (
	"context"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// SendMessages is our hook for sending scene messages
var SendMessages runner.PostCommitHook = &sendMessages{}

type sendMessages struct{}

func (h *sendMessages) Order() int { return 10 }

func (h *sendMessages) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	msgs := make([]*models.MsgOut, 0, len(scenes))

	// for each scene gather all our messages
	for s, args := range scenes {
		s.SentMsgs = make([]*models.MsgOut, len(args))

		for i, m := range args {
			msg := m.(*models.MsgOut)
			msg.Session = s.Session
			msg.WaitTimeout = s.WaitTimeout
			msg.SprintUUID = s.SprintUUID()

			// mark the last message in the sprint (used for setting timeouts)
			if i == len(args)-1 {
				msg.LastInSprint = true
			}

			s.SentMsgs[i] = msg
		}

		msgs = append(msgs, s.SentMsgs...)
	}

	msgio.QueueMessages(ctx, rt, msgs)
	return nil
}
