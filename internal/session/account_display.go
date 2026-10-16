package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProfileSettings.Label is the human-facing name for an account slot in the TUI
// and other surfaces. The profile key (toml section name) remains the stable id.

// AccountSlotPickerLabel returns the text shown for a named slot in account
// pickers. Empty account means the inherited/default login. Preference order:
// configured label, Claude account email, slot id.
func AccountSlotPickerLabel(cfg *UserConfig, harness, account string) string {
	account = strings.TrimSpace(account)
	if account == "" {
		return "default"
	}
	if cfg != nil {
		if p, ok := cfg.Profiles[account]; ok {
			if label := strings.TrimSpace(p.Label); label != "" {
				return label
			}
		}
	}
	if email := accountEmailForHarness(cfg, harness, account); email != "" {
		return email
	}
	return account
}

// AccountSlotSummaryLine is the single-line label for switch summaries and
// confirmations, optionally including the slot id when the visible label is not
// the id (e.g. email-only or friendly label).
func AccountSlotSummaryLine(cfg *UserConfig, harness, account string) string {
	account = strings.TrimSpace(account)
	if account == "" {
		return "default"
	}
	label := AccountSlotPickerLabel(cfg, harness, account)
	if label == account {
		return label
	}
	return fmt.Sprintf("%s (%s)", label, account)
}

func accountEmailForHarness(cfg *UserConfig, harness, account string) string {
	switch CanonicalSwitchHarnessForUI(harness) {
	case "claude":
		if cfg == nil {
			return ""
		}
		dir := cfg.GetProfileClaudeConfigDir(account)
		return ClaudeLoginEmail(dir)
	case "codex":
		if cfg == nil {
			return ""
		}
		dir := cfg.GetProfileCodexConfigDir(account)
		return CodexLoginEmail(dir)
	default:
		return ""
	}
}

// ClaudeLoginEmail reads the email Claude stores after /login, best-effort.
func ClaudeLoginEmail(configDir string) string {
	if configDir == "" {
		return ""
	}
	dir := ExpandPath(configDir)
	for _, path := range []string{
		filepath.Join(dir, ".claude.json"),
	} {
		if email := emailFromClaudeJSON(path); email != "" {
			return email
		}
	}
	home, _ := os.UserHomeDir()
	if home != "" && filepath.Clean(dir) == filepath.Join(home, ".claude") {
		if email := emailFromClaudeJSON(filepath.Join(home, ".claude.json")); email != "" {
			return email
		}
	}
	return emailFromCredentialsJSON(filepath.Join(dir, ".credentials.json"))
}

func emailFromClaudeJSON(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var root struct {
		OAuth struct {
			EmailAddress string `json:"emailAddress"`
			Email        string `json:"email"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(b, &root) != nil {
		return ""
	}
	if e := strings.TrimSpace(root.OAuth.EmailAddress); e != "" {
		return e
	}
	return strings.TrimSpace(root.OAuth.Email)
}

func emailFromCredentialsJSON(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(b, &root) != nil {
		return ""
	}
	if raw, ok := root["oauthAccount"]; ok {
		var oauth struct {
			EmailAddress string `json:"emailAddress"`
			Email        string `json:"email"`
		}
		if json.Unmarshal(raw, &oauth) == nil {
			if e := strings.TrimSpace(oauth.EmailAddress); e != "" {
				return e
			}
			if e := strings.TrimSpace(oauth.Email); e != "" {
				return e
			}
		}
	}
	return ""
}

// CodexLoginEmail returns the email from codex auth.json when present.
func CodexLoginEmail(codexHome string) string {
	if codexHome == "" {
		return ""
	}
	path := filepath.Join(ExpandPath(codexHome), "auth.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var root struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(b, &root) != nil {
		return ""
	}
	return strings.TrimSpace(root.Email)
}

// RegisterAccountProviderSlot binds a new harness config directory to a named
// account slot and stores a friendly label when the generated slot id would
// otherwise be opaque (e.g. claude-c0e6cb2b). When name is empty a unique
// claude-<8hex> id is allocated. Returns the slot name written to config.
func RegisterAccountProviderSlot(cfg *UserConfig, harness, name, configDir, friendlyLabel string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return "", fmt.Errorf("config_dir is required")
	}
	harness = CanonicalSwitchHarnessForUI(harness)
	if harness != "claude" && harness != "codex" {
		return "", fmt.Errorf("harness %q has no named account slots", harness)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = generateAccountSlotID(harness)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]ProfileSettings{}
	}
	profile := cfg.Profiles[name]
	switch harness {
	case "claude":
		profile.Claude.ConfigDir = configDir
	case "codex":
		profile.Codex.ConfigDir = configDir
	}
	label := strings.TrimSpace(friendlyLabel)
	if label == "" {
		label = strings.TrimSpace(ClaudeLoginEmail(configDir))
	}
	if label == "" && strings.HasPrefix(name, harness+"-") && len(name) > len(harness)+1 {
		// Opaque generated id with no email yet — keep label empty until login.
	} else if label != "" {
		profile.Label = label
	}
	cfg.Profiles[name] = profile
	if err := SaveUserConfig(cfg); err != nil {
		return "", err
	}
	ClearUserConfigCache()
	return name, nil
}

func generateAccountSlotID(harness string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return harness + "-" + hex.EncodeToString(b[:])
}
