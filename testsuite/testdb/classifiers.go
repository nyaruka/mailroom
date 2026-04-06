package testdb

import (
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/v26/core/models"
)

type Classifier struct {
	ID   models.ClassifierID
	UUID assets.ClassifierUUID
}
