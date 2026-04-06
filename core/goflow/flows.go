package goflow

import (
	"sync"

	"github.com/Masterminds/semver"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/definition"
	"github.com/nyaruka/goflow/flows/definition/migrations"
	"github.com/nyaruka/mailroom/v26/runtime"
)

var migConf *migrations.Config
var migConfInit sync.Once

type FlowDefError struct {
	cause error
}

func (e *FlowDefError) Error() string {
	return e.cause.Error()
}

func (e *FlowDefError) Unwrap() error {
	return e.cause
}

// SpecVersion returns the current flow spec version
func SpecVersion() *semver.Version {
	return definition.CurrentSpecVersion
}

// ReadFlow reads a flow from the given JSON definition, migrating it if necessary
func ReadFlow(cfg *runtime.Config, data []byte) (flows.Flow, error) {
	f, err := definition.ReadFlow(data, MigrationConfig(cfg))
	if err != nil {
		return nil, &FlowDefError{cause: err}

	}
	return f, nil
}

// CloneDefinition clones the given flow definition
func CloneDefinition(data []byte, depMapping map[uuids.UUID]uuids.UUID) ([]byte, error) {
	f, err := migrations.Clone(data, depMapping)
	if err != nil {
		return nil, &FlowDefError{cause: err}

	}
	return f, nil
}

// MigrateDefinition migrates the given flow definition to the specified version
func MigrateDefinition(cfg *runtime.Config, data []byte, toVersion *semver.Version) ([]byte, error) {
	// if requested version only differs by patch from current version, use current version
	if toVersion == nil || (toVersion.LessThan(definition.CurrentSpecVersion) && toVersion.Major() == definition.CurrentSpecVersion.Major() && toVersion.Minor() == definition.CurrentSpecVersion.Minor()) {
		toVersion = definition.CurrentSpecVersion
	}

	f, err := migrations.MigrateToVersion(data, toVersion, MigrationConfig(cfg))
	if err != nil {
		return nil, &FlowDefError{cause: err}

	}
	return f, nil
}

// MigrationConfig returns the migration configuration for flows
func MigrationConfig(cfg *runtime.Config) *migrations.Config {
	migConfInit.Do(func() {
		migConf = &migrations.Config{BaseMediaURL: "https://" + cfg.AttachmentDomain}
	})

	return migConf
}
