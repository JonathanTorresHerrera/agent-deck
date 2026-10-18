package events

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
)

// lockDisk serializes append, rotation and counters between processes. ioMu
// also serializes users of this Bus's lock descriptor inside one process.
func (b *Bus) lockDisk() error {
	b.ioMu.Lock()
	if err := syscall.Flock(int(b.lockFile.Fd()), syscall.LOCK_EX); err != nil {
		b.ioMu.Unlock()
		return err
	}
	return nil
}

func (b *Bus) unlockDisk() {
	_ = syscall.Flock(int(b.lockFile.Fd()), syscall.LOCK_UN)
	b.ioMu.Unlock()
}

// activeBounds reads only the first and last line. Open repairs an incomplete
// tail before calling this; a locked writer never exposes a partial line.
func activeBounds(path string) (first, last Cursor, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, 0, 0, err
	}
	size = info.Size()
	if size == 0 {
		return 0, 0, 0, nil
	}
	firstLine, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		return 0, 0, size, err
	}
	frame, err := ParseFrameLine(firstLine[:len(firstLine)-1])
	if err != nil {
		return 0, 0, size, err
	}
	first = frame.Cursor
	buf := make([]byte, 4096)
	end := size
	for end > 0 {
		start := end - int64(len(buf))
		if start < 0 {
			start = 0
		}
		n, readErr := f.ReadAt(buf[:end-start], start)
		if readErr != nil && readErr != io.EOF {
			return 0, 0, size, readErr
		}
		chunk := buf[:n]
		if end == size {
			if n == 0 || chunk[n-1] != '\n' {
				return 0, 0, size, fmt.Errorf("events: incomplete active tail")
			}
			chunk = chunk[:n-1]
		}
		if at := strings.LastIndexByte(string(chunk), '\n'); at >= 0 {
			line := make([]byte, size-(start+int64(at)+1)-1)
			if _, err := f.ReadAt(line, start+int64(at)+1); err != nil && err != io.EOF {
				return 0, 0, size, err
			}
			frame, err := ParseFrameLine(line)
			if err != nil {
				return 0, 0, size, err
			}
			return first, frame.Cursor, size, nil
		}
		end = start
	}
	return first, first, size, nil
}

func (b *Bus) refreshLocked() error {
	path := filepath.Join(b.dir, activeSegmentName)
	first, last, size, err := activeBounds(path)
	if err != nil {
		return err
	}
	var prevInfo os.FileInfo
	if b.activeFile != nil {
		prevInfo, _ = b.activeFile.Stat()
		_ = b.activeFile.Close()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		b.activeFile = nil
		return err
	}
	b.activeFile = f
	if first == 0 {
		sealed, err := listSealedSegments(b.dir)
		if err != nil {
			return err
		}
		if len(sealed) > 0 && sealed[len(sealed)-1].end > b.cursor {
			b.cursor = sealed[len(sealed)-1].end
		}
		checkpoint, err := readCursorCheckpoint(b.dir)
		if err != nil {
			return err
		}
		if checkpoint > b.cursor {
			b.cursor = checkpoint
		}
		b.activeStart = b.cursor + 1
		b.activeFrames = 0
	} else {
		checkpoint, err := readCursorCheckpoint(b.dir)
		if err != nil {
			return err
		}
		if last < checkpoint {
			return fmt.Errorf("events: active cursor %d precedes checkpoint %d", last, checkpoint)
		}
		if last != b.cursor {
			b.activeFrames = b.recountActiveLocked(prevInfo, first, size)
		}
		b.cursor = last
		b.activeStart = first
	}
	b.activeBytes = size
	return nil
}

// recountActiveLocked returns the number of lines in the active file after
// another writer appended to it. When the file is the one this Bus last saw
// (same inode, same first frame) and only grew, just the appended byte range
// is read and its lines are added to the known count: a busy profile's
// active segment is megabytes, and re-reading all of it on every sync tick
// after a foreign append cost the notify daemon megabytes per second
// (#2770). A rotation, a shrink or any doubt falls back to the full count.
func (b *Bus) recountActiveLocked(prevInfo os.FileInfo, first Cursor, size int64) int {
	from := b.activeBytes
	if prevInfo != nil && b.activeFile != nil && first == b.activeStart &&
		from > 0 && size >= from && prevInfo.Size() >= from {
		if info, err := b.activeFile.Stat(); err == nil && os.SameFile(prevInfo, info) {
			if n, ok := countNewlinesFrom(b.activeFile, from, size); ok {
				return b.activeFrames + n
			}
		}
	}
	return countLines(b.dir, activeSegmentName)
}

// countNewlinesFrom counts newlines in f's byte range [from, to). The byte
// just before from must be a newline (the known count ended on a whole
// line); otherwise ok is false and the caller recounts the whole file.
func countNewlinesFrom(f *os.File, from, to int64) (int, bool) {
	var edge [1]byte
	if _, err := f.ReadAt(edge[:], from-1); err != nil || edge[0] != '\n' {
		return 0, false
	}
	countedBytes.Add(1)
	buf := make([]byte, 64*1024)
	n := 0
	for off := from; off < to; {
		want := min(int64(len(buf)), to-off)
		got, err := f.ReadAt(buf[:want], off)
		countedBytes.Add(int64(got))
		n += bytes.Count(buf[:got], []byte{'\n'})
		off += int64(got)
		if err != nil && (err != io.EOF || off < to) {
			return 0, false
		}
	}
	return n, true
}

// countedBytes counts bytes read to count active-file lines; tests use it to
// prove a sync tick after a foreign append reads only the appended range.
var countedBytes atomic.Int64

const dropsFileName = "drops.count"
const cursorFileName = "cursor.state"

func readCursorCheckpoint(dir string) (Cursor, error) {
	data, err := os.ReadFile(filepath.Join(dir, cursorFileName))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	return Cursor(value), err
}

func writeCursorCheckpoint(dir string, cursor Cursor) error {
	tmp := filepath.Join(dir, "cursor.tmp."+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, []byte(strconv.FormatUint(uint64(cursor), 10)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, cursorFileName))
}

func (b *Bus) diskDropsLocked() (uint64, error) {
	data, err := os.ReadFile(filepath.Join(b.dir, dropsFileName))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

func (b *Bus) persistDropsLocked() error {
	current := b.dropped.Load()
	if current == b.persistedDrops {
		return nil
	}
	previous, err := b.diskDropsLocked()
	if err != nil {
		return err
	}
	tmp := filepath.Join(b.dir, "drops.tmp."+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, []byte(strconv.FormatUint(previous+current-b.persistedDrops, 10)+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(b.dir, dropsFileName)); err != nil {
		return err
	}
	b.persistedDrops = current
	return nil
}
