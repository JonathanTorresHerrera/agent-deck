package ui

import (
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
	"github.com/stretchr/testify/assert"
)

// Viewers known by device (fork: PC / iPad / Mac) are named on the row and in
// the preview instead of being counted or shown by user name.

func deviceViewers() []tmux.Viewer {
	return []tmux.Viewer{
		{Name: "/dev/pts/160", TTY: "/dev/pts/160", Width: 120, Height: 40, User: "jtorres", Device: "iPad"},
		{Name: "/dev/pts/157", TTY: "/dev/pts/157", Width: 200, Height: 60, User: "jtorres", Device: "PC"},
	}
}

func TestViewersBadge_NamesDevices(t *testing.T) {
	assert.Equal(t, " "+viewersEye+" iPad+PC", viewersBadge(deviceViewers(), true))
	assert.Equal(t, " "+viewersEye+" iPad", viewersBadge(deviceViewers()[:1], true))
}

func TestViewersBadge_CountsWhenAnyDeviceIsUnknown(t *testing.T) {
	mixed := deviceViewers()
	mixed[1].Device = ""
	assert.Equal(t, " "+viewersEye+" 2", viewersBadge(mixed, true), "a row never mixes a device with a count")
	assert.Equal(t, " "+viewersEye+" 1", viewersBadge([]tmux.Viewer{{Name: "/dev/pts/3", User: "ashesh"}}, true))
	assert.Equal(t, "", viewersBadge(deviceViewers(), false))
	assert.Equal(t, "", viewersBadge(nil, true))
}

func TestFitViewersBadge_DevicesThenCountThenEye(t *testing.T) {
	viewers := deviceViewers()
	full := " " + viewersEye + " iPad+PC"
	count := " " + viewersEye + " 2"
	eye := " " + viewersEye

	got, width := fitViewersBadge(viewers, true, false, -1)
	assert.Equal(t, cellWidth(full), width)
	assert.Contains(t, got, "iPad+PC")

	got, width = fitViewersBadge(viewers, true, false, cellWidth(full))
	assert.Equal(t, cellWidth(full), width, "exactly enough room keeps the devices")
	assert.Contains(t, got, "iPad+PC")

	got, width = fitViewersBadge(viewers, true, false, cellWidth(full)-1)
	assert.Equal(t, cellWidth(count), width, "one cell short of the devices falls back to the count")
	assert.Contains(t, got, "2")
	assert.NotContains(t, got, "iPad")

	got, width = fitViewersBadge(viewers, true, false, cellWidth(count)-1)
	assert.Equal(t, cellWidth(eye), width, "then the eye alone")
	assert.Contains(t, got, viewersEye)
	assert.NotContains(t, got, "2")

	got, width = fitViewersBadge(viewers, true, false, cellWidth(eye)-1)
	assert.Equal(t, "", got, "then nothing")
	assert.Equal(t, 0, width)
}

func TestViewersLine_NamesDevices(t *testing.T) {
	now := time.Unix(1789724900, 0)
	viewers := deviceViewers()
	viewers[0].Activity = now.Add(-3 * time.Minute)
	viewers[1].Activity = now.Add(-5 * time.Second)
	assert.Equal(t, viewersEye+" viewers: iPad (120x40, 3m ago) · PC (200x60, active 5s ago)", viewersLine(viewers, true, now))
	assert.Equal(t, "iPad (120x40, 3m ago) · PC (200x60, active 5s ago)", viewersText(viewers, true, "", now))
}
