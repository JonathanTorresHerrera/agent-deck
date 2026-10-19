package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// JevDefaultAccount reads only the registry's public slot mapping. It never
// opens a provider credential file. Missing/invalid registries mean inherit.
func JevDefaultAccount(provider string) string {
	root := os.Getenv("JEV_ROUTER_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".aida-router")
	}
	data, err := os.ReadFile(filepath.Join(root, "accounts.json"))
	if err != nil {
		return ""
	}
	var registry struct {
		DefaultSlots map[string]string `json:"defaultSlots"`
		Slots        []struct {
			ID          string `json:"id"`
			Provider    string `json:"provider"`
			DeckAccount string `json:"deckAccount"`
		} `json:"slots"`
	}
	if json.Unmarshal(data, &registry) != nil {
		return ""
	}
	id := registry.DefaultSlots[provider]
	if id == "" {
		return ""
	}
	for _, slot := range registry.Slots {
		if slot.ID == id && slot.Provider == provider {
			return slot.DeckAccount
		}
	}
	return ""
}

func ConfiguredCodexAccountNames(cfg *UserConfig) []string {
	if cfg == nil {
		return nil
	}
	var names []string
	for name := range cfg.Profiles {
		if cfg.GetProfileCodexConfigDir(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
