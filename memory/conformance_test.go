package memory_test

import (
	"testing"

	settings "github.com/faustbrian/go-settings/v2"
	"github.com/faustbrian/go-settings/v2/memory"
	"github.com/faustbrian/go-settings/v2/settingstest"
)

func TestProviderConformance(t *testing.T) {
	t.Parallel()

	settingstest.RunProvider(t, func(*testing.T) settings.Provider {
		return memory.New()
	})
}
