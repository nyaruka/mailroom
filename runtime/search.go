package runtime

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/nyaruka/gocommon/elastic"
)

type Elastic struct {
	Client *elasticsearch.TypedClient
	Writer *elastic.Writer
	Spool  *elastic.Spool
}

func newElastic(cfg *Config) (*Elastic, error) {
	client, err := elastic.NewClient(cfg.Elastic, cfg.ElasticUsername, cfg.ElasticPassword)
	if err != nil {
		return nil, fmt.Errorf("error creating Elasticsearch client: %w", err)
	}

	spool := elastic.NewSpool(client, filepath.Join(cfg.SpoolDir, "elastic"), 30*time.Second)

	return &Elastic{
		Client: client,
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
