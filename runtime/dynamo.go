package runtime

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
)

type Dynamo struct {
	Main    *dynamo.Writer
	History *dynamo.Writer
	Spool   *dynamo.Spool
}

func newDynamo(cfg *Config) (*Dynamo, error) {
	client, err := dynamo.NewClient(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.DynamoEndpoint)
	if err != nil {
		return nil, fmt.Errorf("error creating DynamoDB client: %w", err)
	}

	spool := dynamo.NewSpool(client, filepath.Join(cfg.SpoolDir, "dynamo"), 30*time.Second)

	return &Dynamo{
		Main:    dynamo.NewWriter(client, cfg.DynamoTablePrefix+"Main", 250*time.Millisecond, 1000, spool),
		History: dynamo.NewWriter(client, cfg.DynamoTablePrefix+"History", 250*time.Millisecond, 1000, spool),
		Spool:   spool,
	}, nil
}

func (d *Dynamo) start() error {
	if err := d.Spool.Start(); err != nil {
		return fmt.Errorf("error starting dynamo spool: %w", err)
	}

	d.Main.Start()
	d.History.Start()
	return nil
}

func (d *Dynamo) stop() {
	d.Main.Stop()
	d.History.Stop()
	d.Spool.Stop()
}
