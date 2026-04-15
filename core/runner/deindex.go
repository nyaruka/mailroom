package runner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func DeIndexContacts(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, contactUUIDs []flows.ContactUUID) (any, error) {
	slog.Info("deindexing contacts", "org_id", orgID, "count", len(contactUUIDs))

	deindexed, err := search.DeindexContactsByUUID(ctx, rt, orgID, contactUUIDs)
	if err != nil {
		return nil, fmt.Errorf("error de-indexing contacts in org #%d: %w", orgID, err)
	}

	msgsDeindexed, err := search.DeindexMessagesByContact(ctx, rt, orgID, contactUUIDs)
	if err != nil {
		return nil, fmt.Errorf("error de-indexing messages in org #%d: %w", orgID, err)
	}

	slog.Info("deindexed contacts and messages", "org_id", orgID, "contacts_deindexed", deindexed, "messages_deindexed", msgsDeindexed)

	return map[string]any{"contacts_deindexed": deindexed, "messages_deindexed": msgsDeindexed}, nil
}
