package models_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/assert"
)

func TestChannelEvents(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer rt.DB.MustExec(`DELETE FROM channels_channelevent`)

	// no extra
	e1 := models.NewChannelEvent(
		testdata.Org1.ID,
		models.EventTypeIncomingCall,
		testdata.TwilioChannel.ID,
		testdata.Cathy.ID,
		testdata.Cathy.URNID,
		models.EventStatusHandled,
		nil,
		time.Date(2024, 4, 1, 15, 13, 45, 0, time.UTC),
	)
	err := e1.Insert(ctx, rt.DB)
	assert.NoError(t, err)
	assert.NotZero(t, e1.ID)
	assert.NotNil(t, e1.UUID)
	assert.Equal(t, models.EventStatusHandled, e1.Status)
	assert.Equal(t, null.Map[any]{}, e1.Extra)
	assert.True(t, e1.OccurredOn.Equal(time.Date(2024, 4, 1, 15, 13, 45, 0, time.UTC)))

	assertdb.Query(t, rt.DB, `SELECT event_type, status FROM channels_channelevent WHERE id = $1`, e1.ID).Columns(map[string]any{"event_type": "mo_call", "status": "H"})

	// with extra
	e2 := models.NewChannelEvent(
		testdata.Org1.ID,
		models.EventTypeMissedCall,
		testdata.TwilioChannel.ID,
		testdata.Cathy.ID,
		testdata.Cathy.URNID,
		models.EventStatusPending,
		map[string]any{"duration": 123},
		time.Date(2024, 4, 1, 15, 13, 45, 0, time.UTC),
	)
	err = e2.Insert(ctx, rt.DB)
	assert.NoError(t, err)
	assert.NotZero(t, e2.ID)
	assert.NotNil(t, e2.UUID)
	assert.Equal(t, null.Map[any]{"duration": 123}, e2.Extra)

	assertdb.Query(t, rt.DB, `SELECT event_type, status FROM channels_channelevent WHERE id = $1`, e2.ID).Columns(map[string]any{"event_type": "mo_miss", "status": "P"})

	models.MarkChannelEventHandled(ctx, rt.DB, e2.ID)

	assertdb.Query(t, rt.DB, `SELECT event_type, status FROM channels_channelevent WHERE id = $1`, e2.ID).Columns(map[string]any{"event_type": "mo_miss", "status": "H"})

	asJSON, err := json.Marshal(e2)
	assert.NoError(t, err)

	e3 := &models.ChannelEvent{}
	err = json.Unmarshal(asJSON, e3)
	assert.NoError(t, err)
	assert.Equal(t, null.Map[any]{"duration": float64(123)}, e3.Extra)
	assert.True(t, e2.OccurredOn.Equal(time.Date(2024, 4, 1, 15, 13, 45, 0, time.UTC)))
}
