package session

import (
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
)

func switchRotationRollout(t *testing.T, home, id, cwd string, started time.Time) string {
 t.Helper()
 path := writeRollout(t, home, id, cwd, "", "", started)
 body, err := os.ReadFile(path); require.NoError(t, err)
 body = []byte(strings.Replace(string(body), "2026-09-23T07:59:36.742Z", started.UTC().Format(time.RFC3339Nano), 1))
 require.NoError(t, os.WriteFile(path, body, 0600))
 return path
}

func TestCodexSwitchRotationRefreshesCachedIdentity(t *testing.T) {
 home := withTempHome(t); t.Setenv("CODEX_HOME", filepath.Join(home,"codex")); noPaneProcess(t)
 now := time.Now(); cwd := t.TempDir()
 old, current := "01a14c0c-85c6-7310-b63a-3e450ab5c59a", "01a15413-dbc2-7cd1-ba3b-7e3d425a8b0c"
 switchRotationRollout(t, filepath.Join(home,"codex"),old,cwd,now.Add(-48*time.Hour))
 switchRotationRollout(t, filepath.Join(home,"codex"),current,cwd,now.Add(-time.Minute))
 inst:=&Instance{ID:"rotation", Tool:"codex", Command:"codex",ProjectPath:cwd,CodexSessionID:old,LastStartedAt:now.Add(-time.Hour)}
 inst.UpdateCodexSession(nil)
 require.Equal(t,current,inst.CodexSessionID,"status refresh must follow rotation even without hooks or an open-file probe")
}
