package runtime

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/nyaruka/gocommon/aws/osearch"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

type Elastic struct {
	Client *elasticsearch.TypedClient
	Writer *elastic.Writer
	Spool  *elastic.Spool
}

func newElastic(cfg *Config) (*Elastic, error) {
	esCfg := elasticsearch.Config{Addresses: []string{cfg.Elastic}, Username: cfg.ElasticUsername, Password: cfg.ElasticPassword}

	typedClient, err := elasticsearch.NewTypedClient(esCfg)
	if err != nil {
		return nil, fmt.Errorf("error creating Elasticsearch typed client: %w", err)
	}

	client, err := elasticsearch.NewClient(esCfg)
	if err != nil {
		return nil, fmt.Errorf("error creating Elasticsearch client: %w", err)
	}

	spool := elastic.NewSpool(client, filepath.Join(cfg.SpoolDir, "elastic"), 30*time.Second)

	return &Elastic{
		Client: typedClient,
		Writer: elastic.NewWriter(client, 500, 250*time.Millisecond, 1000, spool),
		Spool:  spool,
	}, nil
}

func (s *Elastic) start() error {
	if err := s.Spool.Start(); err != nil {
		return fmt.Errorf("error starting elastic spool: %w", err)
	}

	s.Writer.Start()
	return nil
}

func (s *Elastic) stop() {
	s.Writer.Stop()
	s.Spool.Stop()
}

type OpenSearch struct {
	Client *opensearchapi.Client
	Writer *osearch.Writer
	Spool  *osearch.Spool
}

func newOpenSearch(cfg *Config) (*OpenSearch, error) {
	client, err := osearch.NewClient(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.OSEndpoint)
	if err != nil {
		return nil, fmt.Errorf("error creating OpenSearch client: %w", err)
	}

	spool := osearch.NewSpool(client, filepath.Join(cfg.SpoolDir, "opensearch"), 30*time.Second)

	return &OpenSearch{
		Client: client,
		Writer: osearch.NewWriter(client, 500, 250*time.Millisecond, 1000, spool),
		Spool:  spool,
	}, nil
}

func (s *OpenSearch) start() error {
	if err := s.Spool.Start(); err != nil {
		return fmt.Errorf("error starting opensearch spool: %w", err)
	}

	s.Writer.Start()
	return nil
}

func (s *OpenSearch) stop() {
	s.Writer.Stop()
	s.Spool.Stop()
}
