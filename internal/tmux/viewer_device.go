package tmux

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Viewer device labels (fork, WO-ADK-BACKPORT-01).
//
// A viewers badge that says "jtorres" twice tells nothing when the same
// person runs two dashboards ([instances] allow_multiple = true): one on
// the PC, one from the iPad over Tailscale SSH. The tmux client process of
// each attached terminal inherits its login environment, so an SSH client
// carries SSH_CONNECTION ("<client ip> <port> <server ip> <port>") and a
// local terminal does not. That is enough to name the device:
//
//   - no SSH variables            -> "PC" (a terminal on this machine)
//   - client ip in [viewers].device_labels (config.toml) -> that label
//   - client ip in the built-in table                    -> that label
//   - a tailnet ip (100.64.0.0/10, fd7a:115c:a1e0::/48)  -> the peer's
//     device from `tailscale status --json`: "iPad" / "iPhone" for iOS,
//     "Mac" for macOS, otherwise its tailnet host name
//   - anything else                                      -> the ip itself
//
// The environment is read from /proc/<pid>/environ (Linux; the fork runs
// in WSL), which is readable for the user's own processes. Elsewhere, or
// when it cannot be read, the device stays unknown and the viewer is shown
// by user as before. `tailscale status` is asked at most once a minute.

// viewerDeviceLocal is the label of a terminal on this machine.
const viewerDeviceLocal = "PC"

// defaultDeviceLabels is the built-in client ip -> label table, below the
// config.toml [viewers].device_labels map. The fork's own tailnet: the iPad
// keeps a fixed tailnet address, so it is named even when the tailscale CLI
// cannot be asked (not on PATH, daemon down).
var defaultDeviceLabels = map[string]string{
	"100.74.190.0": "iPad",
}

// viewerDeviceLabels holds the config.toml overrides; nil until set.
var viewerDeviceLabels atomic.Pointer[map[string]string]

// SetViewerDeviceLabels installs the [viewers].device_labels map (client ip
// -> label). A nil or empty map keeps only the built-in table.
func SetViewerDeviceLabels(labels map[string]string) {
	if len(labels) == 0 {
		viewerDeviceLabels.Store(nil)
		return
	}
	m := make(map[string]string, len(labels))
	for ip, label := range labels {
		ip, label = strings.TrimSpace(ip), strings.TrimSpace(label)
		if ip == "" || label == "" {
			continue
		}
		m[ip] = label
	}
	viewerDeviceLabels.Store(&m)
}

// viewerDeviceOverride looks ip up in the config map, then the built-in one.
func viewerDeviceOverride(ip string) (string, bool) {
	if m := viewerDeviceLabels.Load(); m != nil {
		if label, ok := (*m)[ip]; ok {
			return label, true
		}
	}
	label, ok := defaultDeviceLabels[ip]
	return label, ok
}

// clientEnviron reads the environment of a process; ok is false when it
// cannot be read (not Linux, gone, another user's). A var so tests can
// supply environments without processes.
var clientEnviron = readProcEnviron

func readProcEnviron(pid int) (env map[string]string, ok bool) {
	if runtime.GOOS != "linux" || pid <= 0 {
		return nil, false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return nil, false
	}
	env = map[string]string{}
	for _, kv := range strings.Split(string(data), "\x00") {
		if key, value, found := strings.Cut(kv, "="); found && key != "" {
			env[key] = value
		}
	}
	return env, true
}

// sshClientIP is the address the SSH client connected from, from
// SSH_CONNECTION or (older servers) SSH_CLIENT; "" for a local terminal.
func sshClientIP(env map[string]string) string {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT"} {
		if fields := strings.Fields(env[key]); len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// clientDeviceLabel names the device behind tmux client pid, "" when it
// cannot be told.
func clientDeviceLabel(pid int) string {
	env, ok := clientEnviron(pid)
	if !ok {
		return ""
	}
	ip := sshClientIP(env)
	if ip == "" {
		return viewerDeviceLocal
	}
	return deviceLabelForIP(ip)
}

// deviceLabelForIP names the device at an SSH client address.
func deviceLabelForIP(ip string) string {
	if label, ok := viewerDeviceOverride(ip); ok {
		return label
	}
	if isTailnetIP(ip) {
		if label := tailnetPeerLabel(ip); label != "" {
			return label
		}
	}
	return ip
}

// tailnetCGNAT and tailnetULA are the ranges Tailscale assigns.
var (
	_, tailnetCGNAT, _ = net.ParseCIDR("100.64.0.0/10")
	_, tailnetULA, _   = net.ParseCIDR("fd7a:115c:a1e0::/48")
)

func isTailnetIP(ip string) bool {
	addr := net.ParseIP(ip)
	return addr != nil && (tailnetCGNAT.Contains(addr) || tailnetULA.Contains(addr))
}

// tailnetPeer is the part of a `tailscale status --json` node the labels
// need. HostName can be a placeholder ("localhost" on iOS), so DNSName is
// preferred for the name.
type tailnetPeer struct {
	HostName     string   `json:"HostName"`
	OS           string   `json:"OS"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
}

// tailscaleStatusJSON runs `tailscale status --json`. A var so tests can
// supply a status without a tailnet.
var tailscaleStatusJSON = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
}

const (
	tailnetCacheTTL     = time.Minute
	tailnetQueryTimeout = 2 * time.Second
)

// tailnetCache memoizes the peer list, failures included, for tailnetCacheTTL:
// viewers are listed on every status pass and the CLI must not be spawned
// per pass.
var tailnetCache struct {
	mu    sync.Mutex
	at    time.Time
	peers []tailnetPeer
}

// resetTailnetCache forgets the cached peers (tests).
func resetTailnetCache() {
	tailnetCache.mu.Lock()
	defer tailnetCache.mu.Unlock()
	tailnetCache.at = time.Time{}
	tailnetCache.peers = nil
}

// tailnetPeers returns the tailnet's nodes (self included), cached.
func tailnetPeers() []tailnetPeer {
	tailnetCache.mu.Lock()
	defer tailnetCache.mu.Unlock()
	if !tailnetCache.at.IsZero() && time.Since(tailnetCache.at) < tailnetCacheTTL {
		return tailnetCache.peers
	}
	tailnetCache.at = time.Now()
	tailnetCache.peers = nil
	ctx, cancel := context.WithTimeout(context.Background(), tailnetQueryTimeout)
	defer cancel()
	out, err := tailscaleStatusJSON(ctx)
	if err != nil {
		return nil
	}
	var status struct {
		Self tailnetPeer            `json:"Self"`
		Peer map[string]tailnetPeer `json:"Peer"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return nil
	}
	peers := make([]tailnetPeer, 0, len(status.Peer)+1)
	if len(status.Self.TailscaleIPs) > 0 {
		peers = append(peers, status.Self)
	}
	for _, p := range status.Peer {
		peers = append(peers, p)
	}
	tailnetCache.peers = peers
	return peers
}

// tailnetPeerLabel names the tailnet node that owns ip, "" when none does.
func tailnetPeerLabel(ip string) string {
	for _, p := range tailnetPeers() {
		for _, candidate := range p.TailscaleIPs {
			if candidate == ip {
				return peerDeviceLabel(p)
			}
		}
	}
	return ""
}

// peerDeviceLabel is the short device name of a tailnet node.
func peerDeviceLabel(p tailnetPeer) string {
	name := p.DNSName
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	if name == "" || strings.EqualFold(name, "localhost") {
		name = p.HostName
	}
	if strings.EqualFold(name, "localhost") {
		name = ""
	}
	hint := strings.ToLower(name + " " + p.HostName + " " + p.DNSName)
	switch strings.ToLower(p.OS) {
	case "ios":
		if strings.Contains(hint, "ipad") {
			return "iPad"
		}
		return "iPhone"
	case "macos", "darwin":
		return "Mac"
	}
	return name
}
