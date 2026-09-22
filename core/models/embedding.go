package models

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
)

// Embedding is a vector embedding, written to and read from the database in pgvector's text form,
// e.g. [0.1,0.2,0.3], so that no pgvector driver support is required. Because it's sent as text, SQL
// parameters of this type need an explicit cast, e.g. :embedding::vector.
type Embedding []float32

func (e Embedding) Value() (driver.Value, error) {
	if e == nil {
		return nil, nil
	}

	b := &strings.Builder{}
	b.WriteByte('[')
	for i, v := range e {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String(), nil
}

func (e *Embedding) Scan(value any) error {
	if value == nil {
		*e = nil
		return nil
	}

	var s string
	switch typed := value.(type) {
	case []byte:
		s = string(typed)
	case string:
		s = typed
	default:
		return fmt.Errorf("failed type assertion to []byte or string: %T", value)
	}

	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return fmt.Errorf("%s is not a valid vector value", s)
	}
	s = s[1 : len(s)-1]
	if strings.TrimSpace(s) == "" {
		*e = Embedding{}
		return nil
	}

	parts := strings.Split(s, ",")
	vals := make(Embedding, len(parts))
	for i, part := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return fmt.Errorf("error parsing vector value: %w", err)
		}
		vals[i] = float32(f)
	}
	*e = vals
	return nil
}
