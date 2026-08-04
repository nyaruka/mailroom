package testdb

import (
	"testing"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/require"
)

type Knowledge struct {
	ID   models.KnowledgeID
	UUID models.KnowledgeUUID
}

type Shortcut struct {
	ID   models.ShortcutID
	UUID uuids.UUID
}

// InsertKnowledge inserts a knowledge source
func InsertKnowledge(t *testing.T, rt *runtime.Runtime, org *Org, uuid models.KnowledgeUUID, typ models.KnowledgeType, name string, status models.KnowledgeStatus) *Knowledge {
	var id models.KnowledgeID
	err := rt.DB.Get(&id,
		`INSERT INTO tickets_knowledge(org_id, uuid, name, knowledge_type, config, status, num_items, num_chunks, is_system, is_active, created_on, modified_on, created_by_id, modified_by_id)
		VALUES($1, $2, $3, $4, '{}', $5, 0, 0, FALSE, TRUE, NOW(), NOW(), 1, 1) RETURNING id`, org.ID, uuid, name, typ, status,
	)
	require.NoError(t, err)
	return &Knowledge{ID: id, UUID: uuid}
}

// InsertKnowledgeChunk inserts a knowledge chunk
func InsertKnowledgeChunk(t *testing.T, rt *runtime.Runtime, knowledge *Knowledge, itemKey uuids.UUID, itemName, text string, embedding models.Embedding) models.KnowledgeChunkID {
	var id models.KnowledgeChunkID
	err := rt.DB.Get(&id,
		`INSERT INTO tickets_knowledgechunk(knowledge_id, item_key, item_name, text, embedding)
		VALUES($1, $2, $3, $4, $5::vector) RETURNING id`, knowledge.ID, itemKey, itemName, text, embedding,
	)
	require.NoError(t, err)
	return id
}

// InsertShortcut inserts a shortcut
func InsertShortcut(t *testing.T, rt *runtime.Runtime, org *Org, uuid uuids.UUID, name, text string) *Shortcut {
	var id models.ShortcutID
	err := rt.DB.Get(&id,
		`INSERT INTO tickets_shortcut(org_id, uuid, name, text, is_system, is_active, created_on, modified_on, created_by_id, modified_by_id)
		VALUES($1, $2, $3, $4, FALSE, TRUE, NOW(), NOW(), 1, 1) RETURNING id`, org.ID, uuid, name, text,
	)
	require.NoError(t, err)
	return &Shortcut{ID: id, UUID: uuid}
}
