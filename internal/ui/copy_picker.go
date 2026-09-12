package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// copyField is one label/value pair shown in the PREVIEW pane that the user can
// copy on its own.
//
// Why a picker exists at all: the deck runs with mouse reporting enabled
// (tea.EnableMouseCellMotion), so a mouse drag inside the preview pane is
// consumed by the TUI and never reaches the terminal's own text selection.
// Every value rendered there — the session ID above all — is therefore
// unreachable without a keyboard path. `C` used to copy one fixed multi-line
// block; this picker makes each value individually copyable while keeping that
// block as its last entry.
type copyField struct {
	label string
	value string
	// key is a stable accelerator derived from the field's identity, never
	// from its position. Positional digits looked tidier but were unlearnable:
	// the entry list changes shape per session (a worktree adds Repo/Branch, a
	// session with no detected ID drops Session ID), so "C then 5" would mean a
	// different field on the next session. A label-derived key means "C then a"
	// is always the full info block and "C then i" is always the session ID.
	key string
}

// copyFieldKeys assigns each label its stable accelerator. Multi-repo paths are
// numbered instead (they have no distinct identity to key off) and fall back to
// arrow navigation past nine.
var copyFieldKeys = map[string]string{
	"Session ID":       "i",
	"Name":             "n",
	"Path":             "p",
	"Repo":             "r",
	"Branch":           "b",
	"Model":            "m",
	"Notes":            "o",
	"All info (block)": "a",
}

// copyFieldSelectedMsg is emitted when the user picks a field. Home turns it
// into the actual clipboard command so the picker stays free of I/O.
type copyFieldSelectedMsg struct {
	label        string
	value        string
	sessionTitle string
}

// copyFieldPathDigits numbers the multi-repo path entries.
const copyFieldPathDigits = "123456789"

// buildCopyFields lists the copyable values of the PREVIEW pane in the order
// they are rendered there. Blank values are dropped, so the picker never
// offers an entry that would put an empty string on the clipboard.
func buildCopyFields(inst *session.Instance) []copyField {
	if inst == nil {
		return nil
	}

	var fields []copyField
	addKeyed := func(label, value, key string) {
		if strings.TrimSpace(value) != "" {
			fields = append(fields, copyField{label: label, value: value, key: key})
		}
	}
	add := func(label, value string) {
		addKeyed(label, value, copyFieldKeys[label])
	}

	add("Session ID", inst.DisplaySessionID())
	add("Name", inst.Title)

	if inst.IsMultiRepo() {
		for i, p := range inst.AllProjectPaths() {
			key := ""
			if i < len(copyFieldPathDigits) {
				key = string(copyFieldPathDigits[i])
			}
			addKeyed(fmt.Sprintf("Path %d", i+1), p, key)
		}
	} else if inst.IsWorktree() {
		add("Repo", inst.WorktreeRepoRoot)
		path := inst.WorktreePath
		if path == "" {
			path = inst.ProjectPath
		}
		add("Path", path)
		add("Branch", inst.WorktreeBranch)
	} else {
		add("Path", inst.ProjectPath)
	}

	if session.SupportsLaunchModel(inst.Tool) {
		info := inst.LaunchModelInfo()
		model := info.Model
		if model == "" {
			model = info.ModelID
		}
		add("Model", model)
	}

	add("Notes", inst.Notes)

	// The full labelled block — exactly what `C` copied before this picker
	// existed, kept so the old muscle memory still has a destination.
	add("All info (block)", buildSessionInfoForCopy(inst))

	return fields
}

// copyRowChrome is the width a list row spends before its value: the two-column
// cursor prefix, the two-column accelerator, the label padded to labelWidth, and
// the two spaces separating label from value. View composes rows from exactly
// these parts, so the value budget is inner minus this.
func copyRowChrome(labelWidth int) int {
	return len("> ") + len("x ") + labelWidth + len("  ")
}

// formatCopyValue renders one entry's value for the list row: always a single
// line, never wider than room columns.
//
// Multi-line values (Notes, the info block) collapse to their first line plus a
// "+N more" marker. The marker itself can be wider than the whole budget on a
// minimum-width dialog with a many-line value, which is why the result is
// truncated again at the end rather than trusting the arithmetic — without that
// final clamp the row runs past the dialog and lipgloss wraps it.
func formatCopyValue(value string, room int) string {
	extraLines := strings.Count(value, "\n")
	if extraLines == 0 {
		return truncateVisible(value, room)
	}

	head := room - len(fmt.Sprintf(" +%d more", extraLines))
	if head < 4 {
		// Keep a few columns of the value itself, so a narrow dialog shows
		// "Repo: /re… +2 more" rather than a bare marker.
		head = 4
	}
	first := strings.SplitN(value, "\n", 2)[0]
	return truncateVisible(truncateVisible(first, head)+fmt.Sprintf(" +%d more", extraLines), room)
}

// CopyFieldPicker is the modal list of copyable PREVIEW values.
type CopyFieldPicker struct {
	visible      bool
	width        int
	height       int
	cursor       int
	fields       []copyField
	sessionTitle string
}

// NewCopyFieldPicker creates an empty, hidden picker.
func NewCopyFieldPicker() *CopyFieldPicker {
	return &CopyFieldPicker{}
}

// Show opens the picker for inst. It reports false (and stays hidden) when the
// session has no copyable values, so the caller can surface an error instead of
// opening an empty dialog.
//
// Every method on this type tolerates a nil receiver: Home values built
// directly in tests (rather than through NewHome) leave the pointer nil, and
// updateSizes reaches it on the very first WindowSizeMsg.
func (d *CopyFieldPicker) Show(inst *session.Instance) bool {
	if d == nil {
		return false
	}
	fields := buildCopyFields(inst)
	if len(fields) == 0 {
		// Close rather than just decline, so a failed Show can never leave a
		// previous session's entries on screen.
		d.Hide()
		return false
	}
	d.fields = fields
	d.cursor = 0
	d.visible = true
	if inst != nil {
		d.sessionTitle = inst.Title
	}
	return true
}

// Hide closes the picker.
func (d *CopyFieldPicker) Hide() {
	if d == nil {
		return
	}
	d.visible = false
	d.fields = nil
	d.cursor = 0
}

// IsVisible reports whether the picker is open.
func (d *CopyFieldPicker) IsVisible() bool {
	return d != nil && d.visible
}

// SetSize updates the dimensions used to centre the dialog.
func (d *CopyFieldPicker) SetSize(width, height int) {
	if d == nil {
		return
	}
	d.width = width
	d.height = height
}

// selectCmd closes the picker and emits the chosen field.
func (d *CopyFieldPicker) selectCmd(i int) tea.Cmd {
	if i < 0 || i >= len(d.fields) {
		return nil
	}
	field := d.fields[i]
	title := d.sessionTitle
	d.Hide()
	return func() tea.Msg {
		return copyFieldSelectedMsg{
			label:        field.label,
			value:        field.value,
			sessionTitle: title,
		}
	}
}

// Update handles a keypress while the picker is open.
func (d *CopyFieldPicker) Update(msg tea.KeyMsg) (*CopyFieldPicker, tea.Cmd) {
	if !d.IsVisible() {
		return d, nil
	}

	switch key := msg.String(); key {
	case "esc", "q", "ctrl+c":
		d.Hide()
		return d, nil

	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}

	case "down", "j":
		if d.cursor < len(d.fields)-1 {
			d.cursor++
		}

	case "home", "g":
		d.cursor = 0

	case "end", "G":
		d.cursor = len(d.fields) - 1

	case "enter":
		return d, d.selectCmd(d.cursor)

	default:
		for i, f := range d.fields {
			if f.key != "" && f.key == key {
				return d, d.selectCmd(i)
			}
		}
	}

	return d, nil
}

// View renders the picker centred over the deck.
func (d *CopyFieldPicker) View() string {
	if !d.IsVisible() {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorCyan)
	dimStyle := lipgloss.NewStyle().Foreground(ColorComment)
	labelStyle := lipgloss.NewStyle().Foreground(ColorText)
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)

	dialogWidth := 64
	if d.width > 0 && d.width < dialogWidth+10 {
		dialogWidth = d.width - 10
		if dialogWidth < 36 {
			dialogWidth = 36
		}
	}
	inner := dialogWidth - 4

	// Widest label, so the values line up in a column.
	labelWidth := 0
	for _, f := range d.fields {
		if n := len(f.label); n > labelWidth {
			labelWidth = n
		}
	}

	var content strings.Builder
	content.WriteString(titleStyle.Render("Copy from preview"))
	content.WriteString("\n")
	content.WriteString(dimStyle.Render(strings.Repeat("-", inner)))
	content.WriteString("\n\n")

	for i, f := range d.fields {
		prefix := "  "
		if i == d.cursor {
			prefix = "> "
		}
		quick := "  "
		if f.key != "" {
			quick = f.key + " "
		}

		label := fmt.Sprintf("%-*s", labelWidth, f.label)
		room := inner - copyRowChrome(labelWidth)
		if room < 8 {
			room = 8
		}

		line := prefix + quick + label + "  " + formatCopyValue(f.value, room)
		if i == d.cursor {
			content.WriteString(selectedStyle.Render(line))
		} else {
			content.WriteString(labelStyle.Render(line))
		}
		content.WriteString("\n")
	}

	content.WriteString("\n")
	content.WriteString(dimStyle.Render("j/k Navigate  letter Jump  Enter Copy  Esc Cancel"))

	dialogStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorCyan).
		Background(ColorBg).
		Padding(1, 2).
		Width(dialogWidth)

	return lipgloss.Place(
		d.width,
		d.height,
		lipgloss.Center,
		lipgloss.Center,
		dialogStyle.Render(content.String()),
	)
}
