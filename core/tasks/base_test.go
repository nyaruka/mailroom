package tasks_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// task ID for tests which call Perform directly instead of going through a queue
const testTaskID = tasks.TaskID("01981fa0-3c74-7d6c-8000-1a2b3c4d5e6f")

func TestRecordComplete(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	// batches queued before owner UUIDs were added can't determine completion
	legacy := &tasks.BatchTask{}
	last, known := legacy.RecordComplete(ctx, rt, "01981fa0-0001-7000-8000-000000000000")
	assert.False(t, last)
	assert.False(t, known)

	b := &tasks.BatchTask{BatchOwnerUUID: "8677d4ea-895c-40fd-b6d9-e1eccd7d8ed4", TotalBatches: 3}

	last, known = b.RecordComplete(ctx, rt, "01981fa0-0001-7000-8000-000000000000")
	assert.False(t, last)
	assert.True(t, known)

	// completing the same batch again changes nothing
	last, known = b.RecordComplete(ctx, rt, "01981fa0-0001-7000-8000-000000000000")
	assert.False(t, last)
	assert.True(t, known)

	last, known = b.RecordComplete(ctx, rt, "01981fa0-0002-7000-8000-000000000000")
	assert.False(t, last)
	assert.True(t, known)

	// last batch of the set to complete
	last, known = b.RecordComplete(ctx, rt, "01981fa0-0003-7000-8000-000000000000")
	assert.True(t, last)
	assert.True(t, known)

	// a batch without a total still records completion but reports unknown until a batch with a total completes
	c1 := &tasks.BatchTask{BatchOwnerUUID: "50d61890-a760-4c00-bd85-c60bb0a5e5b7"}
	last, known = c1.RecordComplete(ctx, rt, "01981fa0-0004-7000-8000-000000000000")
	assert.False(t, last)
	assert.False(t, known)

	c2 := &tasks.BatchTask{BatchOwnerUUID: "50d61890-a760-4c00-bd85-c60bb0a5e5b7", TotalBatches: 2}
	last, known = c2.RecordComplete(ctx, rt, "01981fa0-0005-7000-8000-000000000000")
	assert.True(t, last)
	assert.True(t, known)
}

func TestReadTask(t *testing.T) {
	task, err := tasks.ReadTask("populate_group", []byte(`{
		"group_id": 23,
		"query": "gender = F"
	}`))
	require.NoError(t, err)

	typedTask := task.(*tasks.PopulateGroup)
	assert.Equal(t, models.GroupID(23), typedTask.GroupID)
	assert.Equal(t, "gender = F", typedTask.Query)
}
