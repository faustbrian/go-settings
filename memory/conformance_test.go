package memory_test

import (
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
	"github.com/faustbrian/golib/pkg/settings/settingstest"
)

func TestProviderConformance(t *testing.T) {
	t.Parallel()

	settingstest.RunProvider(t, func(*testing.T) settings.Provider {
		return memory.New()
	})
}
