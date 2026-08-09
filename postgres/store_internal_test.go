package postgres

import (
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
)

func TestNullableDataAndAuditRedactionFollowPersistedState(t *testing.T) {
	value := settings.Record{State: settings.StateValue, Data: []byte("value")}
	cleared := settings.Record{State: settings.StateCleared, Data: []byte("must-not-persist")}
	missing := settings.Record{State: settings.StateMissing, Data: []byte("must-not-persist")}
	if got := string(nullableData(value)); got != "value" {
		t.Fatalf("value data = %q", got)
	}
	if nullableData(cleared) != nil || nullableData(missing) != nil {
		t.Fatal("non-value state retained persisted data")
	}

	for _, test := range []struct {
		name      string
		record    settings.Record
		present   bool
		sensitive bool
		state     settings.State
		redacted  bool
		data      string
	}{
		{name: "absent", record: value, present: false, sensitive: true, state: settings.StateMissing},
		{name: "public value", record: value, present: true, state: settings.StateValue, data: "value"},
		{name: "secret value", record: value, present: true, sensitive: true, state: settings.StateValue, redacted: true},
		{name: "secret cleared", record: cleared, present: true, sensitive: true, state: settings.StateCleared},
		{name: "secret missing", record: missing, present: true, sensitive: true, state: settings.StateMissing},
	} {
		audit := auditValue(test.record, test.present, test.sensitive)
		if audit.State != test.state || audit.Redacted != test.redacted || string(audit.Data) != test.data {
			t.Errorf("%s audit = %+v", test.name, audit)
		}
	}
}
