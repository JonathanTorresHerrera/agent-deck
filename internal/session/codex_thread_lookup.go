package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

// CodexThreadBinding is one stored session as the Codex notify writer sees
// it: its id, its Codex binding and the paths it owns.
type CodexThreadBinding struct {
	InstanceID     string
	Profile        string
	Tool           string
	CodexSessionID string
	Archived       bool
	paths          *Instance // ProjectPath, AdditionalPaths and the multi-repo dir only
}

// CodexCompatible reports whether the stored tool is Codex-family.
func (b CodexThreadBinding) CodexCompatible() bool { return IsCodexCompatible(b.Tool) }

// CwdIsForeign reports whether cwd is provably outside every path the stored
// session owns, by the same rule hook readers apply (hookCwdIsForeign). An
// empty cwd is no evidence and never foreign.
func (b CodexThreadBinding) CwdIsForeign(cwd string) bool {
	return b.paths != nil && b.paths.hookCwdIsForeign(cwd)
}

// CodexThreadLookup is what the session stores say about one Codex thread
// and about the instance a notify's environment names.
type CodexThreadLookup struct {
	// Owners are the unarchived Codex-family sessions bound to the thread.
	Owners []CodexThreadBinding
	// Env is the env instance's own row; nil when no readable store has it.
	Env *CodexThreadBinding
}

// LookupCodexThreadBindings reads every profile's session store, read-only
// (committed WAL state included, nothing written, no checkpoint), for the
// sessions bound to threadID and for envInstanceID's own row. It is one
// targeted query per profile, so it is cheap enough for the notify hook,
// which runs on every Codex turn end. An error means no store could be read
// at all; a store that fails among readable ones is skipped.
func LookupCodexThreadBindings(threadID, envInstanceID string) (CodexThreadLookup, error) {
	var out CodexThreadLookup
	threadID, envInstanceID = strings.TrimSpace(threadID), strings.TrimSpace(envInstanceID)
	profiles, err := ListProfiles()
	if err != nil || len(profiles) == 0 {
		profiles = []string{GetEffectiveProfile("")}
	}
	readable := 0
	var errs error
	for _, profile := range profiles {
		dbPath, err := GetDBPathForProfile(profile)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		db, err := statedb.OpenReadOnlyLive(dbPath)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		rows, err := db.CodexBindingRows(threadID, envInstanceID)
		_ = db.Close()
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("profile %s: %w", profile, err))
			continue
		}
		readable++
		for _, r := range rows {
			b := codexThreadBindingFromRow(profile, r)
			if r.ID == envInstanceID && out.Env == nil {
				env := b
				out.Env = &env
			}
			if threadID != "" && b.CodexSessionID == threadID && !b.Archived && b.CodexCompatible() {
				out.Owners = append(out.Owners, b)
			}
		}
	}
	if readable == 0 {
		if errs == nil {
			errs = errors.New("no profile store")
		}
		return out, fmt.Errorf("codex thread lookup: no readable session store: %w", errs)
	}
	return out, nil
}

func codexThreadBindingFromRow(profile string, r statedb.CodexBindingRow) CodexThreadBinding {
	var td struct {
		MultiRepoEnabled bool     `json:"multi_repo_enabled"`
		AdditionalPaths  []string `json:"additional_paths"`
		MultiRepoTempDir string   `json:"multi_repo_temp_dir"`
	}
	_ = json.Unmarshal(r.ToolData, &td)
	return CodexThreadBinding{
		InstanceID:     r.ID,
		Profile:        profile,
		Tool:           r.Tool,
		CodexSessionID: strings.TrimSpace(r.CodexSessionID),
		Archived:       r.Archived,
		// Mirrors how storage loads these fields (convertToInstances).
		paths: &Instance{
			ProjectPath:      ExpandPath(fixMalformedTildePath(r.ProjectPath)),
			AdditionalPaths:  td.AdditionalPaths,
			MultiRepoEnabled: td.MultiRepoEnabled,
			MultiRepoTempDir: td.MultiRepoTempDir,
		},
	}
}
