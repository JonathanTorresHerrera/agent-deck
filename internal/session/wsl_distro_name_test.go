package session

import "testing"

func TestDistroFromHostRoot(t *testing.T) {
	cases := map[string]string{
		`\\wsl.localhost\Ubuntu\`:         "Ubuntu",
		`\\wsl.localhost\Ubuntu-24.04\`:   "Ubuntu-24.04",
		"\\\\wsl.localhost\\Ubuntu\\\r\n": "Ubuntu",
		`\\wsl$\Debian\`:                  "Debian",
		`C:\`:                             "",
		"":                                "",
	}
	for in, want := range cases {
		if got := distroFromHostRoot(in); got != want {
			t.Errorf("distroFromHostRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

// The environment wins when it is set; the UNC spelling of a distro path is built from it.
func TestHostPathSpellings_DistroPathUsesTheEnvironmentName(t *testing.T) {
	prev := hostSpellingsEnabled
	hostSpellingsEnabled = func() bool { return true }
	t.Cleanup(func() { hostSpellingsEnabled = prev })
	t.Setenv("WSL_DISTRO_NAME", "Fedora")
	got := hostPathSpellings("/home/user/proj")
	if len(got) != 1 || got[0] != `\\wsl.localhost\Fedora\home\user\proj` {
		t.Fatalf("got %q", got)
	}
}
