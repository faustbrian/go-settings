package memory_test

import (
	"testing"

	settings "github.com/faustbrian/go-settings"
	"github.com/faustbrian/go-settings/memory"
	"github.com/faustbrian/go-settings/settingstest"
)

func TestProviderConformance(t *testing.T) {
	t.Parallel()

	settingstest.RunProvider(t, func(*testing.T) settings.Provider {
		return memory.New()
	})
}
