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

func TestRecordStarted(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// a batch without an owner UUID (shouldn't happen) is never the first of its set
	noOwner := &tasks.BatchTask{}
	assert.False(t, noOwner.RecordStarted(ctx, rt))

	b1 := &tasks.BatchTask{BatchOwnerUUID: "8677d4ea-895c-40fd-b6d9-e1eccd7d8ed4", TotalBatches: 3}
	b2 := &tasks.BatchTask{BatchOwnerUUID: "8677d4ea-895c-40fd-b6d9-e1eccd7d8ed4", TotalBatches: 3}

	// only the first batch of the set to start is told it was first
	assert.True(t, b1.RecordStarted(ctx, rt))
	assert.False(t, b1.RecordStarted(ctx, rt))
	assert.False(t, b2.RecordStarted(ctx, rt))
}

func TestRecordComplete(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// a batch without an owner UUID (shouldn't happen) is never the last of its set
	noOwner := &tasks.BatchTask{}
	assert.False(t, noOwner.RecordComplete(ctx, rt, "01981fa0-0001-7000-8000-000000000000"))

	b := &tasks.BatchTask{BatchOwnerUUID: "8677d4ea-895c-40fd-b6d9-e1eccd7d8ed4", TotalBatches: 3}

	assert.False(t, b.RecordComplete(ctx, rt, "01981fa0-0001-7000-8000-000000000000"))

	// completing the same batch again changes nothing
	assert.False(t, b.RecordComplete(ctx, rt, "01981fa0-0001-7000-8000-000000000000"))

	assert.False(t, b.RecordComplete(ctx, rt, "01981fa0-0002-7000-8000-000000000000"))

	// last batch of the set to complete
	assert.True(t, b.RecordComplete(ctx, rt, "01981fa0-0003-7000-8000-000000000000"))

	// a batch without a total (shouldn't happen) still records completion but can't consider itself the last
	c1 := &tasks.BatchTask{BatchOwnerUUID: "50d61890-a760-4c00-bd85-c60bb0a5e5b7"}
	assert.False(t, c1.RecordComplete(ctx, rt, "01981fa0-0004-7000-8000-000000000000"))

	c2 := &tasks.BatchTask{BatchOwnerUUID: "50d61890-a760-4c00-bd85-c60bb0a5e5b7", TotalBatches: 2}
	assert.True(t, c2.RecordComplete(ctx, rt, "01981fa0-0005-7000-8000-000000000000"))
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
