package session

import (
	"bytes"
	"log/slog"
	"testing"
)

// captureSessionLogJSON swaps sessionLog for a JSON handler and returns its
// buffer, so a test can assert on the decoded attribute values of what was
// logged. Ported from upstream's conductor_log_injection_test.go (#2876);
// the fork carries only this helper, not that file's conductor cases.
func captureSessionLogJSON(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := sessionLog
	sessionLog = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { sessionLog = orig })
	return &buf
}
