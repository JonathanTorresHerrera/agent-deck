package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Seat display names come from Jev's seat registry (accounts.json "slots").
// Jev derives the public name positionally per provider (claude-1, claude-2,
// ...), the same rule deck_account_headroom/switch use, so the header matches
// the name JT sees everywhere else. The email is read from the seat home's
// .claude.json oauthAccount.emailAddress and nothing else from that file.

type jevSlot struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	DeckAccount string `json:"deckAccount"`
	Home        string `json:"home"`
}

type seatLabelCache struct {
	mu      sync.Mutex
	path    string
	mtime   time.Time
	checked time.Time
	labels  map[string]string
}

var seatLabels = &seatLabelCache{}

func jevAccountsPath() string {
	if p := os.Getenv("AGENT_DECK_JEV_ACCOUNTS_FILE"); p != "" {
		return p
	}
	home := os.Getenv("JEV_ROUTER_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".aida-router")
	}
	return filepath.Join(home, "accounts.json")
}

func seatEmail(home string) string {
	if home == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		OAuthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return cfg.OAuthAccount.EmailAddress
}

func buildSeatLabels(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var reg struct {
		Slots []jevSlot `json:"slots"`
	}
	if json.Unmarshal(b, &reg) != nil {
		return nil
	}
	labels := map[string]string{}
	pos := map[string]int{}
	for _, s := range reg.Slots {
		if s.Provider == "" {
			continue
		}
		pos[s.Provider]++
		if s.DeckAccount == "" {
			continue
		}
		label := s.Provider + "-" + strconv.Itoa(pos[s.Provider])
		if s.Provider == "claude" {
			if email := seatEmail(s.Home); email != "" {
				label += " (" + email + ")"
			}
		}
		labels[s.DeckAccount] = label
	}
	return labels
}

// seatDisplayLabel maps a stored Deck account to "claude-5 (user@host)".
// It falls back to the raw account when Jev's registry is missing or has no
// entry. The registry is re-read at most every 30s and only when it changed.
func seatDisplayLabel(account string) string {
	if account == "" {
		return ""
	}
	c := seatLabels
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	path := jevAccountsPath()
	if path != c.path || now.Sub(c.checked) > 30*time.Second {
		c.checked = now
		if st, err := os.Stat(path); err == nil {
			if path != c.path || !st.ModTime().Equal(c.mtime) {
				c.labels = buildSeatLabels(path)
				c.mtime = st.ModTime()
			}
		} else {
			c.labels = nil
		}
		c.path = path
	}
	if l, ok := c.labels[account]; ok && l != "" {
		return l
	}
	return account
}
