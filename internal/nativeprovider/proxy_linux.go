//go:build linux

package nativeprovider

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

// Proxy forwards exactly one bounded typed request. It never retries, spawns,
// sends terminal input, changes a socket, or creates runtime configuration.
type Proxy struct {
	options               Options
	gate                  chan struct{}
	authorityHeld         atomic.Bool
	observationGeneration atomic.Uint64
	verifiedGeneration    atomic.Uint64
}

func (p *Proxy) sourceReady() bool {
	generation := p.observationGeneration.Load()
	return generation != 0 && p.verifiedGeneration.Load() == generation
}

func New(options Options) (*Proxy, error) {
	if options.Timeout <= 0 || options.Timeout > 5*time.Second || !validBinding(options.Binding) || !uuid.MatchString(options.Incarnation) || !uuid.MatchString(options.Epoch) || !validSource(options.Source) || options.PeerPID <= 0 || options.PeerUID != uint32(os.Geteuid()) {
		return nil, ErrClosed
	}
	p := &Proxy{options: options, gate: make(chan struct{}, 1)}
	if p.paths() != nil {
		return nil, ErrClosed
	}
	return p, nil
}

func statIdentity(info os.FileInfo) (FileIdentity, bool) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileIdentity{}, false
	}
	return FileIdentity{uint64(s.Dev), s.Ino, s.Uid}, true
}
func (p *Proxy) paths() error {
	name := p.options.SocketPath
	if !filepath.IsAbs(name) || filepath.Clean(name) != name || len(name) > 103 {
		return ErrClosed
	}
	directory := filepath.Dir(name)
	if directory == "/" {
		return ErrClosed
	}
	for path := directory; ; path = filepath.Dir(path) {
		info, e := os.Lstat(path)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrClosed
		}
		identity, ok := statIdentity(info)
		if !ok {
			return ErrClosed
		}
		if path == directory {
			if identity != p.options.Directory || identity.UID != p.options.PeerUID || info.Mode().Perm() != 0700 || info.Mode()&(os.ModeSticky|os.ModeSetgid|os.ModeSetuid) != 0 {
				return ErrClosed
			}
		} else {
			if identity.UID != 0 && identity.UID != p.options.PeerUID {
				return ErrClosed
			}
			if info.Mode().Perm()&0022 != 0 && !(identity.UID == 0 && info.Mode()&os.ModeSticky != 0) {
				return ErrClosed
			}
		}
		if path == "/" {
			break
		}
	}
	info, e := os.Lstat(name)
	if e != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 || info.Mode()&(os.ModeSticky|os.ModeSetgid|os.ModeSetuid) != 0 {
		return ErrClosed
	}
	identity, ok := statIdentity(info)
	stat, typed := info.Sys().(*syscall.Stat_t)
	if !ok || !typed || stat.Nlink != 1 || identity != p.options.Socket || identity.UID != p.options.PeerUID {
		return ErrClosed
	}
	return nil
}
func (p *Proxy) peer(conn *net.UnixConn) error {
	raw, e := conn.SyscallConn()
	if e != nil {
		return ErrClosed
	}
	valid := false
	if raw.Control(func(fd uintptr) {
		cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		valid = err == nil && cred.Uid == p.options.PeerUID && cred.Pid == p.options.PeerPID
	}) != nil || !valid {
		return ErrClosed
	}
	return nil
}

func (p *Proxy) Exchange(ctx context.Context, request Request, original *Request) (Response, error) {
	var empty Response
	if p == nil || ctx == nil {
		return empty, ErrClosed
	}
	// Encode/decode makes a private snapshot; callers cannot replace pointer
	// fields while this operation is waiting for the single-flight gate.
	raw, e := EncodeRequest(request)
	if e != nil {
		return empty, ErrClosed
	}
	request, e = DecodeRequest(raw)
	if e != nil {
		return empty, ErrClosed
	}
	var originalCopy *Request
	if original != nil {
		b, err := EncodeRequest(*original)
		if err != nil {
			return empty, ErrClosed
		}
		r, err := DecodeRequest(b)
		if err != nil {
			return empty, ErrClosed
		}
		originalCopy = &r
	}
	if request.Binding != p.options.Binding || request.Incarnation != p.options.Incarnation || request.Epoch != p.options.Epoch {
		return empty, ErrClosed
	}
	// A valid revoke attempt closes future local actions before any wait/dial.
	// Only an exact native ack establishes that the native gate is revoked.
	if request.Operation == "revoke" {
		p.authorityHeld.Store(true)
	}
	var observation uint64
	if request.Operation == "observe" {
		observation = p.observationGeneration.Add(1)
		if observation == 0 {
			p.authorityHeld.Store(true)
			return empty, ErrClosed
		}
	}
	ctx, cancel := context.WithTimeout(ctx, p.options.Timeout)
	defer cancel()
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return empty, ErrClosed
	}
	action := request.Operation == "answer" || request.Operation == "stop"
	if action && (p.authorityHeld.Load() || !p.sourceReady()) {
		return empty, ErrClosed
	}
	if ctx.Err() != nil || p.paths() != nil {
		return empty, ErrClosed
	}
	if action && !AuthorityCurrent(*request.Authority, time.Now()) {
		return empty, ErrClosed
	}
	dialer := net.Dialer{}
	connection, e := dialer.DialContext(ctx, "unix", p.options.SocketPath)
	if e != nil {
		return empty, ErrClosed
	}
	defer connection.Close()
	conn, ok := connection.(*net.UnixConn)
	if !ok || p.peer(conn) != nil || p.paths() != nil {
		return empty, ErrClosed
	}
	deadline, _ := ctx.Deadline()
	if action {
		a, _ := parseTime(request.Authority.ExpiresAt)
		c, _ := parseTime(request.Authority.CommandExpiresAt)
		if a.Before(deadline) {
			deadline = a
		}
		if c.Before(deadline) {
			deadline = c
		}
	}
	if conn.SetDeadline(deadline) != nil || ctx.Err() != nil || (action && (p.authorityHeld.Load() || !p.sourceReady() || !AuthorityCurrent(*request.Authority, time.Now()))) {
		return empty, ErrClosed
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	frame, e := Frame(raw)
	if e != nil {
		return empty, ErrClosed
	}
	// From the first write attempt onwards a mutation can have entered. All
	// uncertainty remains uncertainty; neither this proxy nor its caller retries.
	failure := ErrClosed
	if action || request.Operation == "revoke" {
		failure = ErrUncertain
	}
	// Even a lost revoke response holds this local capability. Only the native
	// acknowledged response proves serialized revocation; uncertainty never
	// permits another action while the native outcome is unknown.
	if action && (p.authorityHeld.Load() || !p.sourceReady()) {
		return empty, ErrClosed
	}
	for len(frame) > 0 {
		n, err := conn.Write(frame)
		if err != nil || n == 0 {
			return empty, failure
		}
		frame = frame[n:]
	}
	if conn.CloseWrite() != nil {
		return empty, failure
	}
	var prefix [4]byte
	if _, e = io.ReadFull(conn, prefix[:]); e != nil {
		return empty, failure
	}
	size := binary.BigEndian.Uint32(prefix[:])
	if size < 1 || size > MaxPayload {
		return empty, failure
	}
	body := make([]byte, int(size))
	if _, e = io.ReadFull(conn, body); e != nil {
		return empty, failure
	}
	var tail [1]byte
	n, err := conn.Read(tail[:])
	if n != 0 || err != io.EOF {
		return empty, failure
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) || (action && !AuthorityCurrent(*request.Authority, time.Now())) || p.paths() != nil {
		return empty, failure
	}
	response, e := DecodeResponse(body, request, p.options.Source, originalCopy)
	if e != nil {
		return empty, failure
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) || (action && !AuthorityCurrent(*request.Authority, time.Now())) {
		return empty, failure
	}
	if response.Status == "observed" {
		p.verifiedGeneration.Store(observation)
	}
	return response, nil
}
