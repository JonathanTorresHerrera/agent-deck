package tmux

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tailnet as `tailscale status --json` reports it: an iOS node whose
// HostName is the placeholder "localhost" (its DNS name carries "ipad"), a
// macOS node, a Linux workstation and a Windows PC.
const tailnetStatusFixture = `{
  "Self": {"HostName": "pc-wsl", "OS": "linux", "DNSName": "pc-wsl.tailf9a91a.ts.net.", "TailscaleIPs": ["100.121.193.3", "fd7a:115c:a1e0::e32:c104"]},
  "Peer": {
    "k1": {"HostName": "localhost", "OS": "iOS", "DNSName": "ipad173.tailf9a91a.ts.net.", "TailscaleIPs": ["100.74.190.0", "fd7a:115c:a1e0::4532:be01"]},
    "k2": {"HostName": "Jons-MacBook", "OS": "macOS", "DNSName": "jons-macbook.tailf9a91a.ts.net.", "TailscaleIPs": ["100.90.1.1"]},
    "k3": {"HostName": "corsair-workstation", "OS": "linux", "DNSName": "corsair-workstation.tailf9a91a.ts.net.", "TailscaleIPs": ["100.81.131.20"]},
    "k4": {"HostName": "localhost", "OS": "iOS", "DNSName": "iphone-of-jt.tailf9a91a.ts.net.", "TailscaleIPs": ["100.90.2.2"]},
    "k5": {"HostName": "WIN-23SMF28K7JN", "OS": "windows", "DNSName": "win-23smf28k7jn.tailf9a91a.ts.net.", "TailscaleIPs": ["100.102.138.122"]}
  }
}`

// stubViewerDevice replaces the process-environment and tailscale sources
// for one test and restores them after. envs maps pid -> environment; a pid
// not in it is "unreadable".
func stubViewerDevice(t *testing.T, envs map[int]map[string]string, status string, statusErr error) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	origEnv, origStatus := clientEnviron, tailscaleStatusJSON
	clientEnviron = func(pid int) (map[string]string, bool) {
		env, ok := envs[pid]
		return env, ok
	}
	tailscaleStatusJSON = func(context.Context) ([]byte, error) {
		calls.Add(1)
		if statusErr != nil {
			return nil, statusErr
		}
		return []byte(status), nil
	}
	resetTailnetCache()
	SetViewerDeviceLabels(nil)
	t.Cleanup(func() {
		clientEnviron, tailscaleStatusJSON = origEnv, origStatus
		resetTailnetCache()
		SetViewerDeviceLabels(nil)
	})
	return calls
}

func TestClientDeviceLabel_LocalTerminalIsPC(t *testing.T) {
	calls := stubViewerDevice(t, map[int]map[string]string{
		10: {"TERM": "xterm-256color", "WT_SESSION": "abc"},
	}, tailnetStatusFixture, nil)
	assert.Equal(t, "PC", clientDeviceLabel(10))
	assert.Equal(t, int32(0), calls.Load(), "a local terminal never asks tailscale")
}

func TestClientDeviceLabel_UnreadableEnvironmentIsUnknown(t *testing.T) {
	stubViewerDevice(t, map[int]map[string]string{}, tailnetStatusFixture, nil)
	assert.Equal(t, "", clientDeviceLabel(99), "unknown device falls back to the user in DisplayName")
	assert.Equal(t, "", clientDeviceLabel(0))
}

func TestClientDeviceLabel_BuiltInIPadWithoutTailscale(t *testing.T) {
	calls := stubViewerDevice(t, map[int]map[string]string{
		20: {"SSH_CONNECTION": "100.74.190.0 54374 100.121.193.3 22", "SSH_TTY": "/dev/pts/155"},
	}, "", errors.New("tailscale: not found"))
	assert.Equal(t, "iPad", clientDeviceLabel(20))
	assert.Equal(t, int32(0), calls.Load(), "the built-in table answers before tailscale is asked")
}

func TestClientDeviceLabel_ConfigOverrideWins(t *testing.T) {
	stubViewerDevice(t, map[int]map[string]string{
		20: {"SSH_CONNECTION": "100.74.190.0 54374 100.121.193.3 22"},
		21: {"SSH_CONNECTION": "192.168.5.66 50000 192.168.5.50 22"},
	}, tailnetStatusFixture, nil)
	SetViewerDeviceLabels(map[string]string{"100.74.190.0": " Pad ", "192.168.5.66": "OldRig", "": "ignored", "1.2.3.4": "  "})
	assert.Equal(t, "Pad", clientDeviceLabel(20), "config.toml beats the built-in table, trimmed")
	assert.Equal(t, "OldRig", clientDeviceLabel(21), "a LAN address can be named too")
	SetViewerDeviceLabels(nil)
	assert.Equal(t, "iPad", clientDeviceLabel(20), "clearing the map restores the built-in table")
}

func TestClientDeviceLabel_TailnetPeersByOS(t *testing.T) {
	calls := stubViewerDevice(t, map[int]map[string]string{
		30: {"SSH_CONNECTION": "100.90.1.1 1 100.121.193.3 22"},
		31: {"SSH_CONNECTION": "100.81.131.20 1 100.121.193.3 22"},
		32: {"SSH_CLIENT": "100.90.2.2 1 22"},
		33: {"SSH_CONNECTION": "100.102.138.122 1 100.121.193.3 22"},
		34: {"SSH_CONNECTION": "fd7a:115c:a1e0::4532:be01 1 fd7a:115c:a1e0::e32:c104 22"},
	}, tailnetStatusFixture, nil)
	assert.Equal(t, "Mac", clientDeviceLabel(30))
	assert.Equal(t, "corsair-workstation", clientDeviceLabel(31), "other OSes show the tailnet host name")
	assert.Equal(t, "iPhone", clientDeviceLabel(32), "SSH_CLIENT works when SSH_CONNECTION is absent")
	assert.Equal(t, "win-23smf28k7jn", clientDeviceLabel(33), "the DNS label, not the raw HostName")
	assert.Equal(t, "iPad", clientDeviceLabel(34), "a tailnet IPv6 address resolves too (DNS name says ipad)")
	assert.Equal(t, int32(1), calls.Load(), "one tailscale status call serves every lookup within the TTL")
}

func TestClientDeviceLabel_UnknownAddressesStayAddresses(t *testing.T) {
	calls := stubViewerDevice(t, map[int]map[string]string{
		40: {"SSH_CONNECTION": "192.168.5.66 50000 192.168.5.50 22"},
		41: {"SSH_CONNECTION": "100.70.70.70 1 100.121.193.3 22"},
	}, tailnetStatusFixture, nil)
	assert.Equal(t, "192.168.5.66", clientDeviceLabel(40), "a LAN address is shown as is")
	assert.Equal(t, int32(0), calls.Load(), "a non-tailnet address never asks tailscale")
	assert.Equal(t, "100.70.70.70", clientDeviceLabel(41), "a tailnet address no node owns is shown as is")
	assert.Equal(t, int32(1), calls.Load())
}

func TestClientDeviceLabel_TailscaleFailureIsCachedAndHarmless(t *testing.T) {
	calls := stubViewerDevice(t, map[int]map[string]string{
		50: {"SSH_CONNECTION": "100.90.1.1 1 100.121.193.3 22"},
	}, "", errors.New("tailscale: daemon not running"))
	assert.Equal(t, "100.90.1.1", clientDeviceLabel(50))
	assert.Equal(t, "100.90.1.1", clientDeviceLabel(50))
	assert.Equal(t, int32(1), calls.Load(), "a failed status call is not retried on every pass")
}

func TestPeerDeviceLabel(t *testing.T) {
	cases := []struct {
		name string
		peer tailnetPeer
		want string
	}{
		{"ios ipad by dns name", tailnetPeer{HostName: "localhost", OS: "iOS", DNSName: "ipad173.tailf9a91a.ts.net."}, "iPad"},
		{"ios ipad by host name", tailnetPeer{HostName: "Jons-iPad", OS: "iOS"}, "iPad"},
		{"ios phone", tailnetPeer{HostName: "localhost", OS: "iOS", DNSName: "phone.tailf9a91a.ts.net."}, "iPhone"},
		{"macos", tailnetPeer{HostName: "mbp", OS: "macOS", DNSName: "mbp.tailf9a91a.ts.net."}, "Mac"},
		{"linux by dns label", tailnetPeer{HostName: "Corsair-Workstation", OS: "linux", DNSName: "corsair-workstation.tailf9a91a.ts.net."}, "corsair-workstation"},
		{"no dns falls back to host", tailnetPeer{HostName: "box", OS: "linux"}, "box"},
		{"placeholder everywhere", tailnetPeer{HostName: "localhost", OS: "linux"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, peerDeviceLabel(tc.peer))
		})
	}
}

func TestIsTailnetIP(t *testing.T) {
	assert.True(t, isTailnetIP("100.74.190.0"))
	assert.True(t, isTailnetIP("100.127.255.255"))
	assert.True(t, isTailnetIP("fd7a:115c:a1e0::4532:be01"))
	assert.False(t, isTailnetIP("100.128.0.1"))
	assert.False(t, isTailnetIP("192.168.5.66"))
	assert.False(t, isTailnetIP("not-an-ip"))
}

func TestParseViewers_DeviceFromClientPID(t *testing.T) {
	out := strings.Join([]string{
		"agentdeck_a\t/dev/pts/157\t/dev/pts/157\t200x60\t1789724772\tjtorres\t0\t575",
		"agentdeck_a\t/dev/pts/160\t/dev/pts/160\t120x40\t1789724800\tjtorres\t0\t9001",
		"agentdeck_a\t/dev/pts/161\t/dev/pts/161\t80x24\t1789724700\tjtorres\t0\t", // older tmux: no pid column value
		"agentdeck_a\t/dev/pts/162\t/dev/pts/162\t80x24\t1789724600\tjtorres\t0",   // older agent-deck: no pid column at all
	}, "\n")
	device := func(pid int) string {
		switch pid {
		case 575:
			return "PC"
		case 9001:
			return "iPad"
		}
		return "unexpected"
	}
	got := parseViewers(out, nil, device)["agentdeck_a"]
	require.Len(t, got, 4)
	assert.Equal(t, 9001, got[0].PID)
	assert.Equal(t, "iPad", got[0].Device)
	assert.Equal(t, "iPad", got[0].DisplayName(), "the device names the viewer")
	assert.Equal(t, "PC", got[1].Device)
	assert.Equal(t, 0, got[2].PID)
	assert.Equal(t, "", got[2].Device, "no pid: no device lookup")
	assert.Equal(t, "jtorres", got[2].DisplayName(), "unknown device falls back to the user")
	assert.Equal(t, "", got[3].Device)

	none := parseViewers(out, nil, nil)["agentdeck_a"]
	assert.Equal(t, "", none[0].Device, "a nil device resolver leaves Device empty")
	assert.Equal(t, 9001, none[0].PID, "the pid is still parsed")
}

func TestViewerLabel_UsesDevice(t *testing.T) {
	v := Viewer{Name: "/dev/pts/160", TTY: "/dev/pts/160", Width: 120, Height: 40, User: "jtorres", Device: "iPad"}
	assert.Equal(t, "iPad (120x40, idle)", v.Label(v.Activity))
	assert.Equal(t, "iPad (120x40, idle)", FormatViewersAt([]Viewer{v}, v.Activity))
}
