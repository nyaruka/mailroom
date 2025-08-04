package runtime

import (
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/nyaruka/gocommon/aws/dynamo"
)

type Writers struct {
	Main    *dynamo.Writer
	History *dynamo.Writer
	Spool   *dynamo.Spool
}

func NewWriters(cfg *Config, cl *dynamodb.Client) *Writers {
	spool := dynamo.NewSpool(cl, cfg.SpoolDir+"/dynamo", 30*time.Second)

	return &Writers{
		Main:    dynamo.NewWriter(cl, cfg.DynamoTablePrefix+"Main", 250*time.Millisecond, 1000, spool),
		History: dynamo.NewWriter(cl, cfg.DynamoTablePrefix+"History", 250*time.Millisecond, 1000, spool),
		Spool:   spool,
	}
}

func (w *Writers) Start(wg *sync.WaitGroup) error {
	if err := w.Spool.Start(wg); err != nil {
		return fmt.Errorf("error starting dynamo spool: %w", err)
	}

	w.Main.Start(wg)
	w.History.Start(wg)
	return nil
}

func (w *Writers) Stop() {
	w.Main.Stop()
	w.History.Stop()
	w.Spool.Stop()
}
