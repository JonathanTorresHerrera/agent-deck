package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Jev owns the ordering, identity and meter. Its status command reads metadata
// only; the Deck account value stays the stable profile key after renumbering.
type jevEditMeter struct {
	Slot        string `json:"slot"`
	Provider    string `json:"provider"`
	DeckAccount string `json:"deckAccount"`
	Default     bool   `json:"default"`
	Identity    struct {
		Email string `json:"email"`
	} `json:"identity"`
	Budget struct {
		UsedPct *float64 `json:"usedPct"`
		Status  string   `json:"status"`
	} `json:"budget"`
}

func jevEditMeters() []jevEditMeter {
	bin := os.Getenv("JEV_ACCOUNTS_BIN")
	if bin == "" {
		bin = filepath.Join(os.Getenv("HOME"), ".local", "bin", "jev-accounts")
	}
	if _, err := os.Stat(bin); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "status", "--json").Output()
	if err != nil {
		return nil
	}
	var status struct {
		Meters []jevEditMeter `json:"meters"`
	}
	if json.Unmarshal(out, &status) != nil {
		return nil
	}
	return status.Meters
}

func jevAccountPillsForInstance(account, harness string, configured []string) (opts, labels []string, cursor int, ok bool) {
	provider := session.CanonicalSwitchHarnessForUI(harness)
	if provider != "claude" && provider != "codex" {
		return nil, nil, 0, false
	}
	allowed := make(map[string]bool, len(configured))
	for _, name := range configured {
		allowed[name] = true
	}
	meters := jevEditMeters()
	position := 0
	for _, meter := range meters {
		if meter.Provider != provider {
			continue
		}
		position++
		if meter.DeckAccount != "" && !allowed[meter.DeckAccount] {
			continue
		}
		name := fmt.Sprintf("%s-%d", provider, position)
		if meter.Default {
			name += " (default)"
		}
		if meter.Identity.Email != "" {
			name += " · " + jevSafeEmail(meter.Identity.Email)
		}
		if meter.Budget.UsedPct != nil {
			name += fmt.Sprintf(" · %s %.0f%%", meter.Budget.Status, *meter.Budget.UsedPct)
		} else if meter.Budget.Status != "" {
			name += " · " + meter.Budget.Status
		}
		opts = append(opts, meter.DeckAccount)
		labels = append(labels, name)
		if meter.DeckAccount == account {
			cursor = len(opts) - 1
		}
	}
	if len(opts) == 0 {
		return nil, nil, 0, false
	}
	for _, value := range opts {
		if value == account {
			return opts, labels, cursor, true
		}
	}
	if account != "" {
		opts = append(opts, account)
		labels = append(labels, account+" (not configured)")
		return opts, labels, len(opts) - 1, true
	}
	for _, meter := range meters {
		if meter.Provider == provider && meter.Default {
			for j, value := range opts {
				if value == meter.DeckAccount {
					return opts, labels, j, true
				}
			}
		}
	}
	return opts, labels, 0, true
}

// The meter strings are displayed as a single pill; strip control characters
// from the email so profile metadata cannot change the terminal layout.
func jevSafeEmail(email string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, email)
}
