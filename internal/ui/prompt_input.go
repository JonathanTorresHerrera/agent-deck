package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// promptSubmitMsg is emitted when the operator submits a one-line prompt from
// the main list (issue #1410). Home routes it to the target session via the
// existing prompt-state-aware send path (the #1409/#1432 composer guard), with
// no attach. Delivery targets the session's default pane — the guarded send
// (deliverToConductorPane) does not address individual tmux windows — so there
// is no per-window target here.
type promptSubmitMsg struct {
	instanceID string
	text       string
}

// PromptInputDialog is a one-line input anchored at the bottom of the list that
// sends a prompt to the highlighted session without attaching (issue #1410,
// Lawrence-Dawson feedback). It mirrors the Search component: a focused
// textinput.Model that consumes keys while visible and surfaces submit/cancel.
type PromptInputDialog struct {
	input      textinput.Model
	visible    bool
	width      int
	height     int
	instanceID string
	title      string
	// Patch 19: askAida switches the same one-line bar to the "Ask Aida"
	// dialog. Enter then emits askAidaSubmitMsg — an empty line included, since
	// empty means "send the default ask" — instead of promptSubmitMsg. Show
	// and Hide reset it, so the prompt-session path never inherits the mode.
	askAida bool
	// Patch 21: "Already asked Aida …" shown above the input when the session
	// has a last_aida_ask record. Empty = no line. Reset by Show and Hide.
	askAidaWarning string
}

const (
	promptInputPlaceholder = "Type a prompt and press Enter to send (Esc to cancel)…"
	// Patch 19: the Ask Aida dialog's placeholder and hint line.
	askAidaPlaceholder = "Optional note for Aida…"
	askAidaHint        = "Enter send · Esc cancel · empty = default ask"
)

// NewPromptInputDialog creates the inline prompt input (hidden).
func NewPromptInputDialog() *PromptInputDialog {
	ti := textinput.New()
	ti.Placeholder = promptInputPlaceholder
	ti.CharLimit = 2000
	ti.Width = 60
	return &PromptInputDialog{input: ti}
}

// Show opens the input targeting the given session and focuses it.
func (d *PromptInputDialog) Show(instanceID, title string) {
	d.visible = true
	d.askAida = false
	d.askAidaWarning = "" // Patch 21
	d.instanceID = instanceID
	d.title = title
	d.input.Placeholder = promptInputPlaceholder
	d.input.SetValue("")
	d.input.Focus()
}

// ShowAskAida opens the bar as the Ask Aida dialog for the given session.
// Patch 19.
func (d *PromptInputDialog) ShowAskAida(instanceID, title string) {
	d.Show(instanceID, title)
	d.askAida = true
	d.input.Placeholder = askAidaPlaceholder
}

// SetAskAidaWarning sets the Ask Aida dialog's "already asked" line (empty
// for none). Show and Hide clear it. Patch 21.
func (d *PromptInputDialog) SetAskAidaWarning(line string) {
	if d == nil {
		return
	}
	d.askAidaWarning = line
}

// IsAskAida reports whether the open bar is the Ask Aida dialog. Patch 19.
func (d *PromptInputDialog) IsAskAida() bool { return d.IsVisible() && d.askAida }

// Hide closes the input and blurs it.
func (d *PromptInputDialog) Hide() {
	d.visible = false
	d.askAida = false
	d.askAidaWarning = "" // Patch 21
	d.input.Blur()
	d.instanceID = ""
	d.title = ""
}

// IsVisible reports whether the input is open. Nil-safe: some test paths and
// early-init code construct a Home without this dialog, and IsVisible is called
// from the hot modal-dispatch path on every key.
func (d *PromptInputDialog) IsVisible() bool { return d != nil && d.visible }

// SetSize updates the layout dimensions and the input width.
func (d *PromptInputDialog) SetSize(width, height int) {
	if d == nil {
		return
	}
	d.width = width
	d.height = height
	w := width - 20
	if w < 20 {
		w = 20
	}
	if w > 120 {
		w = 120
	}
	d.input.Width = w
}

// Update handles a key while the input is visible. On Enter with non-empty
// trimmed text it returns a promptSubmitMsg and hides; Esc cancels; all other
// keys feed the textinput.
func (d *PromptInputDialog) Update(msg tea.KeyMsg) (*PromptInputDialog, tea.Cmd) {
	if d == nil || !d.visible {
		return d, nil
	}
	switch msg.String() {
	case "esc":
		d.Hide()
		return d, nil
	case "enter":
		text := strings.TrimSpace(d.input.Value())
		instanceID := d.instanceID
		if d.askAida {
			// Patch 19: an empty note is a real submit (the default ask).
			d.Hide()
			return d, func() tea.Msg {
				return askAidaSubmitMsg{instanceID: instanceID, note: text}
			}
		}
		if text == "" {
			d.Hide()
			return d, nil
		}
		d.Hide()
		return d, func() tea.Msg {
			return promptSubmitMsg{instanceID: instanceID, text: text}
		}
	default:
		var cmd tea.Cmd
		d.input, cmd = d.input.Update(msg)
		return d, cmd
	}
}

// View overlays the prompt bar at the bottom of the (already rendered) list
// body, trimming the body so the composite fits the viewport height.
func (d *PromptInputDialog) View(listBody string) string {
	if d == nil || !d.visible {
		return listBody
	}

	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	dimStyle := lipgloss.NewStyle().Foreground(ColorComment)

	barWidth := d.width - 2
	if barWidth < 1 {
		barWidth = d.width
	}
	label := "Prompt → " + d.title
	hint := "Enter Send   Esc Cancel   (sends without attaching)"
	warning := ""
	if d.askAida {
		// Patch 19
		label = fmt.Sprintf("Ask Aida about %q", d.title)
		hint = askAidaHint
		// Patch 21: the "already asked" line sits between the label and the
		// input, in the warning color.
		if d.askAidaWarning != "" {
			warning = lipgloss.NewStyle().Foreground(ColorYellow).Render(d.askAidaWarning) + "\n"
		}
	}
	bar := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorAccent).
		Padding(0, 1).
		Width(barWidth).
		Render(labelStyle.Render(label) + "\n" + warning + d.input.View() + "\n" +
			dimStyle.Render(hint))

	// Reserve space for the bar at the bottom: trim the list body so the
	// composite stays within the viewport height.
	barHeight := lipgloss.Height(bar)
	bodyLines := strings.Split(listBody, "\n")
	maxBody := d.height - barHeight
	if maxBody < 0 {
		maxBody = 0
	}
	if len(bodyLines) > maxBody {
		bodyLines = bodyLines[:maxBody]
	}
	return strings.Join(bodyLines, "\n") + "\n" + bar
}
