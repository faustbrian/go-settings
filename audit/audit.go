// Package audit provides safe change-history reading contracts.
package audit

import (
	"context"
	"fmt"

	settings "github.com/faustbrian/golib/pkg/settings"
)

// Reader is the history-only provider capability.
type Reader interface {
	History(context.Context, settings.HistoryQuery) ([]settings.ChangeRecord, error)
}

// Read validates definitions and enforces sensitive-value redaction again at
// the public audit boundary, even when a provider returns unsafe bytes.
func Read(ctx context.Context, reader Reader, registry *settings.Registry, query settings.HistoryQuery) ([]settings.ChangeRecord, error) {
	records, err := reader.History(ctx, query)
	if err != nil {
		return nil, err
	}
	result := make([]settings.ChangeRecord, 0, len(records))
	for _, record := range records {
		definition, ok := registry.Lookup(record.Key)
		if !ok {
			return nil, fmt.Errorf("settings audit: unknown historical definition %s", record.Key)
		}
		if definition.CodecID() != record.CodecID {
			return nil, fmt.Errorf("settings audit: incompatible historical codec for %s", record.Key)
		}
		if definition.Sensitive() {
			redact(&record.Before)
			redact(&record.After)
		} else {
			record.Before.Data = append([]byte(nil), record.Before.Data...)
			record.After.Data = append([]byte(nil), record.After.Data...)
		}
		result = append(result, record)
	}
	return result, nil
}

func redact(value *settings.AuditValue) {
	if value.State == settings.StateValue {
		value.Redacted = true
	}
	value.Data = nil
}
