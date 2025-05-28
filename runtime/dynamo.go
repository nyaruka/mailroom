package runtime

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/nyaruka/gocommon/aws/dynamo"
)

type DynamoKey struct {
	PK string `dynamodbav:"PK"`
	SK string `dynamodbav:"SK"`
}

type DynamoItem struct {
	DynamoKey

	OrgID  int            `dynamodbav:"OrgID"`
	TTL    time.Time      `dynamodbav:"TTL,unixtime,omitempty"`
	Data   map[string]any `dynamodbav:"Data"`
	DataGZ []byte         `dynamodbav:"DataGZ,omitempty"`
}

type DynamoTables struct {
	Main    *dynamo.Table[DynamoKey, DynamoItem]
	History *dynamo.Table[DynamoKey, DynamoItem]
}

func NewDynamoClient(cfg *Config) (*dynamodb.Client, error) {
	return dynamo.NewClient(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.DynamoEndpoint)
}

func NewDynamoTables(cfg *Config) (*DynamoTables, error) {
	client, err := NewDynamoClient(cfg)
	if err != nil {
		return nil, err
	}

	return &DynamoTables{
		Main:    dynamo.NewTable[DynamoKey, DynamoItem](client, cfg.DynamoTablePrefix+"Main"),
		History: dynamo.NewTable[DynamoKey, DynamoItem](client, cfg.DynamoTablePrefix+"History"),
	}, nil
}
