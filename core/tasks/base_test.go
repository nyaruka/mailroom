package tasks_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// task ID for tests which call Perform directly instead of going through a queue
const testTaskID = tasks.TaskID("01981fa0-3c74-7d6c-8000-1a2b3c4d5e6f")

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
