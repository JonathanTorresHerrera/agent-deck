//go:build linux

// Qualification-only fixed forwarding process. This testdata program is never
// imported, registered or built by the Deck runtime. It accepts no flags or
// configuration discovery; only the isolated coordinator owns /interop.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	native "github.com/asheshgoplani/agent-deck/internal/nativeprovider"
)

const protocol = "ops-native-chain-fixture-v1"
const directory = "/interop"

var closed = errors.New("native_chain_proxy_failed")
var binding = native.Binding{PersonID: "synthetic-owner", MachineID: "synthetic-machine", DeckSessionID: "synthetic-session"}

type admission struct {
	Protocol string        `json:"protocol"`
	PID      int32         `json:"pid"`
	UID      uint32        `json:"uid"`
	Source   native.Source `json:"source"`
}

type readiness struct {
	Protocol       string         `json:"protocol"`
	PID            int32          `json:"pid"`
	UID            uint32         `json:"uid"`
	Binding        native.Binding `json:"binding"`
	Incarnation    string         `json:"incarnation"`
	Epoch          string         `json:"epoch"`
	NativeThreadID string         `json:"nativeThreadId"`
	Source         native.Source  `json:"source"`
}

func readMetadata(name string, value any) error {
	if name != "admission.json" && name != "readiness.json" {
		return closed
	}
	fd, err := syscall.Open(filepath.Join(directory, name), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return closed
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 8192 {
		return closed
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return closed
	}
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(data) > 8192 {
		return closed
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return closed
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return closed
	}
	return nil
}

func identity(path string) (native.FileIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return native.FileIdentity{}, closed
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return native.FileIdentity{}, closed
	}
	return native.FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, UID: stat.Uid}, nil
}

func publish(name string, value any) error {
	if name != "proxy-ready.json" && name != "proxy-result.json" {
		return closed
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > 8192 {
		return closed
	}
	final := filepath.Join(directory, name)
	pending := filepath.Join(directory, "."+name+".pending")
	fd, err := syscall.Open(pending, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return closed
	}
	file := os.NewFile(uintptr(fd), name)
	n, written := file.Write(data)
	synced := file.Sync()
	ended := file.Close()
	if written != nil || n != len(data) || synced != nil || ended != nil {
		return closed
	}
	if os.Link(pending, final) != nil {
		return closed
	}
	dir, err := os.Open(directory)
	if err != nil {
		return closed
	}
	defer dir.Close()
	if dir.Sync() != nil {
		return closed
	}
	// The coordinator admits only a single-link final name with no pending
	// name. Leave pending behind on any earlier failure, holding readiness.
	return os.Remove(pending)
}

// Response has optional typed fields rather than a direct closed-wire encoder.
// Build only the actual status envelope, then use the real codec as the oracle.
func encode(response native.Response, request native.Request, source native.Source, original *native.Request) ([]byte, error) {
	value := map[string]any{"protocol": response.Protocol, "operation": response.Operation, "requestKey": response.RequestKey,
		"binding": response.Binding, "incarnation": response.Incarnation, "epoch": response.Epoch, "status": response.Status}
	switch response.Status {
	case "observed":
		value["nativeRevision"], value["nativePatchSha256"] = response.NativeRevision, response.NativePatchSHA256
		value["prompt"], value["lifecycle"] = response.Prompt, response.Lifecycle
	case "receipt":
		value["receipt"] = response.Receipt
	case "refused":
		value["code"] = response.Code
	case "revoked":
	default:
		return nil, closed
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, closed
	}
	if _, err = native.DecodeResponse(body, request, source, original); err != nil {
		return nil, closed
	}
	return native.Frame(body)
}

func run() error {
	var admitted admission
	var ready readiness
	if readMetadata("admission.json", &admitted) != nil || readMetadata("readiness.json", &ready) != nil {
		return closed
	}
	if admitted.Protocol != protocol || ready.Protocol != protocol || admitted.PID <= 0 || ready.PID != admitted.PID ||
		admitted.UID != uint32(os.Geteuid()) || ready.UID != admitted.UID || ready.Source != admitted.Source || ready.Binding != binding {
		return closed
	}
	parent, err := identity(directory)
	if err != nil {
		return closed
	}
	socket, err := identity(directory + "/native.sock")
	if err != nil {
		return closed
	}
	proxy, err := native.New(native.Options{SocketPath: directory + "/native.sock", Directory: parent, Socket: socket,
		PeerUID: admitted.UID, PeerPID: admitted.PID, Binding: binding, Incarnation: ready.Incarnation, Epoch: ready.Epoch,
		Source: admitted.Source, Timeout: 5 * time.Second})
	if err != nil {
		return closed
	}
	if _, err := os.Lstat(directory + "/proxy.sock"); !errors.Is(err, os.ErrNotExist) {
		return closed
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: directory + "/proxy.sock", Net: "unix"})
	if err != nil {
		return closed
	}
	defer listener.Close()
	if os.Chmod(directory+"/proxy.sock", 0600) != nil || listener.SetDeadline(time.Now().Add(120*time.Second)) != nil {
		return closed
	}
	if publish("proxy-ready.json", map[string]any{"protocol": protocol, "pid": os.Getpid(), "uid": os.Geteuid(), "nativePid": ready.PID}) != nil {
		return closed
	}
	originals := map[string]native.Request{}
	counts := map[string]int{"observe": 0, "answer": 0, "receipt": 0, "stop": 0}
	lost := false
	for number := 0; number < 16; number++ {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return closed
		}
		done, err := func() (bool, error) {
			defer conn.Close()
			if conn.SetDeadline(time.Now().Add(5*time.Second)) != nil {
				return false, closed
			}
			frame, err := io.ReadAll(io.LimitReader(conn, native.MaxPayload+5))
			if err != nil {
				return false, closed
			}
			body, err := native.Unframe(frame)
			if err != nil {
				return false, closed
			}
			request, err := native.DecodeRequest(body)
			if err != nil {
				return false, closed
			}
			if _, ok := counts[request.Operation]; !ok {
				return false, closed
			}
			counts[request.Operation]++
			var original *native.Request
			if request.Operation == "answer" || request.Operation == "stop" {
				if len(originals) >= 2 || counts[request.Operation] != 1 {
					return false, closed
				}
				originals[request.CommandID] = request
			} else if request.Operation == "receipt" {
				previous, ok := originals[request.CommandID]
				if !ok {
					return false, closed
				}
				original = &previous
			}
			response, err := proxy.Exchange(context.Background(), request, original)
			if err != nil {
				if request.Operation == "answer" && !lost && errors.Is(err, native.ErrUncertain) {
					lost = true
					return false, nil // Preserve the real native lost acknowledgment. Never retry.
				}
				return false, closed
			}
			if response.Status == "observed" && (response.Lifecycle == nil || response.Lifecycle.NativeThreadID != ready.NativeThreadID) {
				return false, closed
			}
			out, err := encode(response, request, admitted.Source, original)
			if err != nil {
				return false, closed
			}
			if n, err := io.Copy(conn, bytes.NewReader(out)); err != nil || n != int64(len(out)) {
				return false, closed
			}
			return request.Operation == "receipt" && response.Status == "receipt" && response.Receipt != nil &&
				response.Receipt.Operation == "stop" && response.Receipt.Outcome == "applied", nil
		}()
		if err != nil {
			return closed
		}
		if done {
			if !lost || counts["answer"] != 1 || counts["stop"] != 1 || counts["receipt"] != 2 || counts["observe"] != 2 {
				return closed
			}
			return publish("proxy-result.json", map[string]any{"protocol": protocol, "passed": true, "requests": counts,
				"lostAnswerAck": true, "nativePid": ready.PID, "nativeSource": admitted.Source, "connections": number + 1})
		}
	}
	return closed
}

func main() {
	if run() != nil {
		println("native_chain_proxy_failed")
		os.Exit(1)
	}
}
