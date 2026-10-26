package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

// [viewers.device_labels] names SSH clients on the viewers badge by the
// address they connect from (fork, WO-ADK-BACKPORT-01).
func TestUserConfigParseViewerDeviceLabels(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := `
[viewers.device_labels]
"100.74.190.0" = "iPad"
"192.168.5.66" = "OldRig"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var config UserConfig
	if _, err := toml.DecodeFile(configPath, &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := config.Viewers.DeviceLabels["100.74.190.0"]; got != "iPad" {
		t.Fatalf("device_labels[100.74.190.0] = %q, want iPad", got)
	}
	if got := config.Viewers.DeviceLabels["192.168.5.66"]; got != "OldRig" {
		t.Fatalf("device_labels[192.168.5.66] = %q, want OldRig", got)
	}
}

// A config without the section leaves the map nil: internal/tmux then keeps
// its built-in table.
func TestUserConfigViewerDeviceLabelsDefaultNil(t *testing.T) {
	var config UserConfig
	if _, err := toml.Decode("[instances]\nallow_multiple = true\n", &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if config.Viewers.DeviceLabels != nil {
		t.Fatalf("device_labels = %v, want nil", config.Viewers.DeviceLabels)
	}
}
