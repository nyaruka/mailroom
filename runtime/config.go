package runtime

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/nyaruka/ezconf"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/goflow/utils"
)

func init() {
	utils.RegisterValidatorAlias("session_storage", "eq=db|eq=s3", func(e validator.FieldError) string { return "is not a valid session storage mode" })
}

// Config is our top level configuration object
type Config struct {
	DB         string `validate:"url,startswith=postgres:"           help:"URL for your Postgres database"`
	ReadonlyDB string `validate:"omitempty,url,startswith=postgres:" help:"URL of optional connection to readonly database instance"`
	DBPoolSize int    `                                              help:"the size of our db pool"`
	Redis      string `validate:"url,startswith=redis:"              help:"URL for your Redis instance"`
	SentryDSN  string `                                              help:"the DSN used for logging errors to Sentry"`

	Address          string `help:"the address to bind our web server to"`
	Port             int    `help:"the port to bind our web server to"`
	AuthToken        string `help:"the token clients will need to authenticate web requests"`
	Domain           string `help:"the domain that mailroom is listening on"`
	AttachmentDomain string `help:"the domain that will be used for relative attachment"`

	BatchWorkers   int `help:"the number of go routines that will be used to handle batch events"`
	HandlerWorkers int `help:"the number of go routines that will be used to handle messages"`

	WebhooksTimeout              int     `help:"the timeout in milliseconds for webhook calls from engine"`
	WebhooksMaxRetries           int     `help:"the number of times to retry a failed webhook call"`
	WebhooksMaxBodyBytes         int     `help:"the maximum size of bytes to a webhook call response body"`
	WebhooksInitialBackoff       int     `help:"the initial backoff in milliseconds when retrying a failed webhook call"`
	WebhooksBackoffJitter        float64 `help:"the amount of jitter to apply to backoff times"`
	WebhooksHealthyResponseLimit int     `help:"the limit in milliseconds for webhook response to be considered healthy"`

	SMTPServer           string `help:"the default SMTP configuration for sending flow emails, e.g. smtp://user%40password@server:port/?from=foo%40gmail.com"`
	DisallowedNetworks   string `help:"comma separated list of IP addresses and networks which engine can't make HTTP calls to"`
	MaxStepsPerSprint    int    `help:"the maximum number of steps allowed per engine sprint"`
	MaxResumesPerSession int    `help:"the maximum number of resumes allowed per engine session"`
	MaxValueLength       int    `help:"the maximum size in characters for contact field values and run result values"`
	SessionStorage       string `validate:"omitempty,session_storage"         help:"where to store session output (s3|db)"`

	Elastic              string `validate:"url" help:"the URL of your ElasticSearch instance"`
	ElasticUsername      string `help:"the username for ElasticSearch if using basic auth"`
	ElasticPassword      string `help:"the password for ElasticSearch if using basic auth"`
	ElasticContactsIndex string `help:"the name of index alias for contacts"`

	AWSAccessKeyID     string `help:"access key ID to use for AWS services"`
	AWSSecretAccessKey string `help:"secret access key to use for AWS services"`
	AWSRegion          string `help:"region to use for AWS services, e.g. us-east-1"`

	DynamoEndpoint    string `help:"DynamoDB service endpoint, e.g. https://dynamodb.us-east-1.amazonaws.com"`
	DynamoTablePrefix string `help:"prefix to use for DynamoDB tables"`

	S3Endpoint          string `help:"S3 service endpoint, e.g. https://s3.amazonaws.com"`
	S3AttachmentsBucket string `help:"S3 bucket to write attachments to"`
	S3SessionsBucket    string `help:"S3 bucket to write flow sessions to"`
	S3Minio             bool   `help:"S3 is actually Minio or other compatible service"`

	CloudwatchNamespace string `help:"the namespace to use for cloudwatch metrics"`
	DeploymentID        string `help:"the deployment identifier to use for metrics"`
	InstanceID          string `help:"the instance identifier to use for metrics"`

	CourierAuthToken       string `help:"the authentication token used for requests to Courier"`
	AndroidCredentialsFile string `help:"path to JSON file with FCM service account credentials used to sync Android relayers"`

	LogLevel slog.Level `help:"the logging level courier should use"`
	UUIDSeed int        `help:"seed to use for UUID generation in a testing environment"`
	Version  string     `help:"the version of this mailroom install"`
}

// NewDefaultConfig returns a new default configuration object
func NewDefaultConfig() *Config {
	hostname, _ := os.Hostname()

	return &Config{
		DB:         "postgres://temba:temba@localhost/temba?sslmode=disable&Timezone=UTC",
		ReadonlyDB: "",
		DBPoolSize: 36,
		Redis:      "redis://localhost:6379/15",

		Address: "localhost",
		Port:    8090,

		BatchWorkers:   4,
		HandlerWorkers: 32,

		WebhooksTimeout:              15000,
		WebhooksMaxRetries:           2,
		WebhooksMaxBodyBytes:         256 * 1024, // 256 KiB
		WebhooksInitialBackoff:       5000,
		WebhooksBackoffJitter:        0.5,
		WebhooksHealthyResponseLimit: 10000,

		SMTPServer:           "",
		DisallowedNetworks:   `127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,fe80::/10`,
		MaxStepsPerSprint:    200,
		MaxResumesPerSession: 250,
		MaxValueLength:       640,
		SessionStorage:       "db",

		Elastic:              "http://localhost:9200",
		ElasticUsername:      "",
		ElasticPassword:      "",
		ElasticContactsIndex: "contacts",

		AWSAccessKeyID:     "",
		AWSSecretAccessKey: "",
		AWSRegion:          "us-east-1",

		DynamoEndpoint:    "", // let library generate it
		DynamoTablePrefix: "Temba",

		S3Endpoint:          "https://s3.amazonaws.com",
		S3AttachmentsBucket: "temba-attachments",
		S3SessionsBucket:    "temba-sessions",

		CloudwatchNamespace: "Temba/Mailroom",
		DeploymentID:        "dev",
		InstanceID:          hostname,

		LogLevel: slog.LevelWarn,
		UUIDSeed: 0,
		Version:  "Dev",
	}
}

func LoadConfig() *Config {
	config := NewDefaultConfig()
	loader := ezconf.NewLoader(config, "mailroom", "Mailroom - handler for RapidPro", []string{"mailroom.toml"})
	loader.MustLoad()

	// ensure config is valid
	if err := config.Validate(); err != nil {
		log.Fatalf("invalid config: %s", err)
	}

	return config
}

// Validate validates the config
func (c *Config) Validate() error {
	if err := utils.Validate(c); err != nil {
		return err
	}

	if _, _, err := c.ParseDisallowedNetworks(); err != nil {
		return fmt.Errorf("unable to parse 'DisallowedNetworks': %w", err)
	}
	return nil
}

// ParseDisallowedNetworks parses the list of IPs and IP networks (written in CIDR notation)
func (c *Config) ParseDisallowedNetworks() ([]net.IP, []*net.IPNet, error) {
	addrs, err := csv.NewReader(strings.NewReader(c.DisallowedNetworks)).Read()
	if err != nil && err != io.EOF {
		return nil, nil, err
	}

	return httpx.ParseNetworks(addrs...)
}
