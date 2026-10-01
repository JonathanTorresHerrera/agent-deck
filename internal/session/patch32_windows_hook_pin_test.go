package session

import "testing"

func TestPatch32_WindowsMountedConfigIsUnpinnable(t *testing.T) {
	origWSL, origDir := isWSLHost, claudeConfigDirForPin
	t.Cleanup(func() { isWSLHost, claudeConfigDirForPin = origWSL, origDir })

	cases := []struct {
		name string
		wsl  bool
		dir  string
		want bool
	}{
		{"wsl, windows-side claude", true, "/mnt/c/Users/jonat/.claude", true},
		{"wsl, account slot on D:", true, "/mnt/d/accounts/claude-2", true},
		{"wsl, linux-side claude", true, "/home/u/.claude", false},
		{"wsl, mnt but not a drive", true, "/mnt/wsl/docker", false},
		{"native linux, /mnt/c dir", false, "/mnt/c/whatever", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isWSLHost = func() bool { return tc.wsl }
			claudeConfigDirForPin = func() string { return tc.dir }
			if got := claudeConfigOnWindowsMount(); got != tc.want {
				t.Fatalf("claudeConfigOnWindowsMount() = %v, want %v", got, tc.want)
			}
			if tc.want {
				exe, err := hookExecutablePath()
				if err != nil || exe != "" {
					t.Fatalf("hookExecutablePath() = %q, %v; want unpinnable", exe, err)
				}
				if HealUsageFeeds(&UserConfig{}) != nil {
					t.Fatal("HealUsageFeeds must not wire statusLines from an unpinnable binary")
				}
			}
		})
	}
}
