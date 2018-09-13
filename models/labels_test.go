package models

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLabels(t *testing.T) {
	ctx := context.Background()
	db := Reset(t)

	labels, err := loadLabels(ctx, db, 1)
	assert.NoError(t, err)

	tcs := []struct {
		ID   LabelID
		Name string
	}{
		{LabelID(9), "Building"},
		{LabelID(8), "Driving"},
	}

	assert.Equal(t, 10, len(labels))
	for i, tc := range tcs {
		label := labels[i].(*Label)
		assert.Equal(t, tc.ID, label.ID())
		assert.Equal(t, tc.Name, label.Name())
	}
}
