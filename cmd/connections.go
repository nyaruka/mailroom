package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// testConnections tests our connections to backing services before we start doing anything with them. We can't
// function at all without Postgres and Valkey so those being unreachable is fatal, whereas writes to services like
// DynamoDB and Elastic are spooled so we can start without them and recover later.
func testConnections(ctx context.Context, rt *runtime.Runtime) error {
	c := rt.Config
	log := slog.With("comp", "mailroom")

	if err := checkDBConnection(rt.DB.DB); err != nil {
		return fmt.Errorf("postgres not reachable: %w", err)
	}
	log.Info("postgres ok")

	if rt.ReadonlyDB != rt.DB.DB {
		if err := checkDBConnection(rt.ReadonlyDB); err != nil {
			return fmt.Errorf("readonly db not reachable: %w", err)
		}
		log.Info("readonly db ok")
	} else {
		log.Warn("no distinct readonly db configured")
	}

	vc := rt.VK.Get()
	defer vc.Close()
	if _, err := valkey.DoWithTimeout(vc, 5*time.Second, "PING"); err != nil {
		return fmt.Errorf("valkey not reachable: %w", err)
	}
	log.Info("valkey ok")

	// test DynamoDB tables
	if err := dynamo.Test(ctx, rt.Dynamo.Main.Client(), c.DynamoTablePrefix+"Main", c.DynamoTablePrefix+"History"); err != nil {
		log.Error("dynamodb not reachable", "error", err)
	} else {
		log.Info("dynamodb ok")
	}

	// test S3 bucket
	if err := rt.S3.Test(ctx, c.S3AttachmentsBucket); err != nil {
		log.Error("attachments bucket not accessible", "error", err)
	} else {
		log.Info("attachments bucket ok")
	}

	// test Elasticsearch
	if ping, err := rt.ES.Client.Ping().Do(ctx); err != nil {
		log.Error("elasticsearch not available", "error", err)
	} else if !ping {
		log.Error("elasticsearch cluster not reachable")
	} else {
		log.Info("elastic ok")
	}

	// the Centrifugo client is built by the runtime; confirm here that the server is reachable and accepts our key
	if err := rt.Centrifugo.Client.Info(ctx); err != nil {
		log.Error("centrifugo not reachable", "error", err)
	} else {
		log.Info("centrifugo ok")
	}

	return nil
}

func checkDBConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}
