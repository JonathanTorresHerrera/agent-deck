package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestEditSessionJevSlotsFollowRegistryAndShowIdentityUsage(t *testing.T) {
	home := setXDGTestHome(t)
	writeXDGTestConfig(t, home, `[claude]
config_dir = "/old"
[profiles.claude-2.claude]
config_dir = "/second"
[profiles.claude-3.claude]
config_dir = "/third"
[profiles.Aida2.claude]
config_dir = "/fourth"
`)
	feed := filepath.Join(home, "status.json")
	bin := filepath.Join(home, "jev-accounts")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat '"+feed+"'\n"), 0o700); err != nil { t.Fatal(err) }
	t.Setenv("JEV_ACCOUNTS_BIN", bin)
	write := func(body string) { t.Helper(); if err := os.WriteFile(feed, []byte(body), 0o600); err != nil { t.Fatal(err) } }
	write(`{"meters":[
{"slot":"claude","provider":"claude","deckAccount":"","default":true,"identity":{"email":"jt@activatems.com"},"budget":{"usedPct":100,"status":"over"}},
{"slot":"claude-2","provider":"claude","deckAccount":"claude-2","default":false,"identity":{"email":"jonathan@torres-herrera.com"},"budget":{"usedPct":93,"status":"near"}},
{"slot":"claude-3","provider":"claude","deckAccount":"claude-3","default":false,"identity":{"email":"Aida@devcodesgpt.com"},"budget":{"usedPct":44,"status":"ok"}}]}`)
	d := NewEditSessionDialog()
	d.Show(&session.Instance{ID: "s1", Tool: "claude", Account: "claude-2"})
	f := editAccountField(t, d)
	if !reflect.DeepEqual(f.pillOptions, []string{"", "claude-2", "claude-3"}) { t.Fatalf("options: %q", f.pillOptions) }
	if f.pillCursor != 1 { t.Fatalf("cursor = %d, want 1", f.pillCursor) }
	for _, want := range []string{"claude-1 (default)", "jt@activatems.com", "claude-2", "jonathan@torres-herrera.com", "93%", "claude-3", "Aida@devcodesgpt.com"} {
		if !strings.Contains(strings.Join(f.pillLabels, " "), want) { t.Errorf("missing %q in %q", want, f.pillLabels) }
	}
	if strings.Contains(strings.Join(f.pillLabels, " "), "inherit") { t.Fatal("Inherit must not be offered") }
	write(`{"meters":[
{"slot":"claude-2","provider":"claude","deckAccount":"claude-2","default":true,"identity":{"email":"jonathan@torres-herrera.com"},"budget":{"usedPct":93,"status":"near"}},
{"slot":"claude-3","provider":"claude","deckAccount":"claude-3","default":false,"identity":{"email":"Aida@devcodesgpt.com"},"budget":{"usedPct":44,"status":"ok"}},
{"slot":"Aida2","provider":"claude","deckAccount":"Aida2","default":false,"identity":{"email":"aida2@devcodesgpt.com"},"budget":{"usedPct":13,"status":"ok"}}]}`)
	d.Show(&session.Instance{ID: "s2", Tool: "claude", Account: "Aida2"})
	f = editAccountField(t, d)
	if !reflect.DeepEqual(f.pillOptions, []string{"claude-2", "claude-3", "Aida2"}) { t.Fatalf("new options: %q", f.pillOptions) }
	if f.pillCursor != 2 { t.Fatalf("new cursor = %d, want 2", f.pillCursor) }
	for _, want := range []string{"claude-1 (default)", "claude-2", "claude-3", "aida2@devcodesgpt.com", "13%"} {
		if !strings.Contains(strings.Join(f.pillLabels, " "), want) { t.Errorf("missing %q in %q", want, f.pillLabels) }
	}
}

func editAccountField(t *testing.T, d *EditSessionDialog) editField {
	t.Helper()
	for _, f := range d.fields { if f.key == session.FieldAccount { return f } }
	t.Fatal("account field missing")
	return editField{}
}
