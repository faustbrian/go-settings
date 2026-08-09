package valkey

import (
	"encoding/json"
	"strings"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
)

func TestDecodeRecordEnforcesEveryCacheContract(t *testing.T) {
	t.Parallel()

	scope := settings.Tenant("acme")
	valid := settings.Record{
		Scope: scope, Key: "fleet/key", State: settings.StateValue,
		Data: []byte("value"), CodecID: "string", CodecVersion: 1, Version: 1,
	}
	for _, test := range []struct {
		name   string
		mutate func(*settings.Record)
	}{
		{name: "scope", mutate: func(record *settings.Record) { record.Scope = settings.Global() }},
		{name: "key", mutate: func(record *settings.Record) { record.Key = "different" }},
		{name: "version", mutate: func(record *settings.Record) { record.Version = 0 }},
		{name: "state", mutate: func(record *settings.Record) { record.State = settings.State(255) }},
		{name: "data", mutate: func(record *settings.Record) { record.Data = make([]byte, (1<<20)+1) }},
		{name: "missing data", mutate: func(record *settings.Record) { record.State = settings.StateMissing }},
		{name: "cleared data", mutate: func(record *settings.Record) { record.State = settings.StateCleared }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			test.mutate(&record)
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeRecord(encoded, scope, valid.Key); err == nil {
				t.Fatal("malformed cached record accepted")
			}
		})
	}

	for _, state := range []settings.State{settings.StateMissing, settings.StateValue, settings.StateCleared} {
		record := valid
		record.State = state
		if state != settings.StateValue {
			record.Data = nil
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeRecord(encoded, scope, valid.Key)
		if err != nil || got.State != state {
			t.Fatalf("valid state %d = (%+v, %v)", state, got, err)
		}
	}
}

func TestDecodeRecordAcceptsExactResourceLimits(t *testing.T) {
	t.Parallel()

	record := settings.Record{
		Scope: settings.Global(), Key: "fleet/key", State: settings.StateValue,
		Data: make([]byte, 1<<20), CodecID: "bytes", CodecVersion: 1, Version: 1,
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeRecord(encoded, record.Scope, record.Key); err != nil {
		t.Fatalf("exact 1 MiB value rejected: %v", err)
	}

	padding := (2 << 20) - len(encoded)
	if padding < 0 {
		t.Fatalf("encoded record unexpectedly exceeds wire limit: %d", len(encoded))
	}
	exactWire := append(append([]byte(nil), encoded...), []byte(strings.Repeat(" ", padding))...)
	if _, err := decodeRecord(exactWire, record.Scope, record.Key); err != nil {
		t.Fatalf("exact 2 MiB wire record rejected: %v", err)
	}
	if _, err := decodeRecord(append(exactWire, ' '), record.Scope, record.Key); err == nil {
		t.Fatal("wire record over 2 MiB accepted")
	}
}
