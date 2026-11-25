package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestAllowAllToolsDefaultsToFalse(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	setDefaults(false)
	if got := viper.GetBool("security.allowAllTools"); got {
		t.Fatal("security.allowAllTools default = true, want false")
	}
}
