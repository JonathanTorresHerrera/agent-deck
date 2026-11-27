package nativeprovider

import "time"

// Pins are supplied by reviewed capability construction, never learned from a
// request or a response. This package has no default socket/config attachment.
type FileIdentity struct { Device uint64; Inode uint64; UID uint32 }
type Options struct {
	SocketPath string
	Directory FileIdentity
	Socket FileIdentity
	PeerUID uint32
	PeerPID int32
	Binding Binding
	Incarnation string
	Epoch string
	Source Source
	Timeout time.Duration
}
