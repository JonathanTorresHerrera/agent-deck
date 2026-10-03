package ui

// Patch 44 (paged footer). The "full" footer adds and drops hints from live
// session state: Fork only exists for 5 minutes after a Claude session is
// detected, Fresh vanishes while a storage reload briefly clears the session
// ID, and "Copy pane" slides into the freed space. At 146 columns each of those
// changes reflowed the whole bar, which read as a flicker. The paged footer
// gives every action a fixed slot (an action that cannot run right now is
// dimmed, never removed), splits the actions over two pages the user arranges,
// and adds a menu (":") that lists every action for the selected row, runs it,
// and moves it between pages.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// footerPageBreak separates page 1 from page 2 in a saved footer layout.
const footerPageBreak = "--"

// Footer row kinds. Each keeps its own layout.
const (
	footerKindSession = "session"
	footerKindGroup   = "group"
)

// Pseudo action IDs for the two footer actions that are not hotkey actions.
const (
	footerActAttach = "attach"
	footerActToggle = "toggle_group"
)

// footerAction is one entry of the footer catalog.
type footerAction struct {
	id    string
	label string // used at 160+ columns and in the menu
	short string // used below 160 columns
	kinds []string
	// key returns the key shown in the chip ("" hides the action: unbound).
	key func(h *Home) string
	// trigger is the key the menu synthesizes to run the action.
	trigger func(h *Home) string
	// applies reports whether the action belongs on this row at all. Only
	// stable facts (tool, sandbox, multi-repo, settings) may decide it, so the
	// bar never reflows while a session's state changes.
	applies func(h *Home, inst *session.Instance) bool
	// ready reports whether the action can run right now; false dims it.
	ready func(inst *session.Instance) bool
}

func footerBoundKey(action string) func(h *Home) string {
	return func(h *Home) string { return h.actionKey(action) }
}

func footerFixedKey(k string) func(h *Home) string {
	return func(*Home) string { return k }
}

// footerToolCanFork reports whether the session's tool supports Agent Deck
// forking at all. Instance.CanFork additionally needs a recently detected
// session; that part only dims the slot.
func footerToolCanFork(inst *session.Instance) bool {
	switch {
	case inst.Tool == "gemini", inst.Tool == "deepseek":
		return false
	case inst.Tool == "opencode", inst.Tool == "pi", inst.Tool == "omp":
		return true
	case session.IsCodexCompatible(inst.Tool), session.IsClaudeCompatible(inst.Tool):
		return true
	}
	return false
}

// footerToolCanRestartFresh mirrors Instance.CanRestartFresh minus the
// "a session ID is known right now" part, which only dims the slot.
func footerToolCanRestartFresh(inst *session.Instance) bool {
	switch {
	case session.IsClaudeCompatible(inst.Tool):
		return true
	case inst.Tool == "gemini", inst.Tool == "opencode", inst.Tool == "codex", inst.Tool == "hermes":
		return true
	case inst.Tool == "deepseek":
		return session.DeepSeekSupportsResume()
	}
	return inst.CanRestartFresh()
}

var sessionKinds = []string{footerKindSession}
var groupKinds = []string{footerKindGroup}
var bothKinds = []string{footerKindSession, footerKindGroup}

// footerCatalog lists every footer action in its default order.
func footerCatalog() []footerAction {
	return []footerAction{
		{id: footerActAttach, label: "Attach", short: "Attach", kinds: sessionKinds,
			key: footerFixedKey("⏎"), trigger: footerFixedKey("enter")},
		{id: footerActToggle, label: "Toggle", short: "Toggle", kinds: groupKinds,
			key: footerFixedKey("Tab"), trigger: footerFixedKey("tab")},
		{id: hotkeyNewSession, label: "New/Quick", short: "New", kinds: bothKinds,
			key: func(h *Home) string {
				return joinHotkeyLabels(h.actionKey(hotkeyNewSession), h.actionKey(hotkeyQuickCreate))
			}},
		{id: hotkeyCreateGroup, label: "Group", short: "Grp", kinds: bothKinds},
		{id: hotkeyRestart, label: "Restart", short: "Restart", kinds: sessionKinds},
		{id: hotkeyRestartFresh, label: "Restart Fresh", short: "Fresh", kinds: sessionKinds,
			applies: func(_ *Home, i *session.Instance) bool { return footerToolCanRestartFresh(i) },
			ready:   func(i *session.Instance) bool { return i.CanRestartFresh() }},
		{id: hotkeyQuickFork, label: "Fork", short: "Fork", kinds: sessionKinds,
			key: func(h *Home) string {
				return joinHotkeyLabels(h.actionKey(hotkeyQuickFork), h.actionKey(hotkeyForkWithOptions))
			},
			applies: func(_ *Home, i *session.Instance) bool { return footerToolCanFork(i) },
			ready:   func(i *session.Instance) bool { return i.CanFork() }},
		{id: hotkeyMCPManager, label: "MCP", short: "MCP", kinds: sessionKinds,
			applies: func(_ *Home, i *session.Instance) bool { return session.ToolSupportsMCPManager(i.Tool) }},
		{id: hotkeyTogglePreview, label: "Preview", short: "Preview", kinds: sessionKinds,
			applies: func(_ *Home, i *session.Instance) bool { return session.ToolSupportsMCPManager(i.Tool) }},
		{id: hotkeySkillsManager, label: "Skills", short: "Skills", kinds: sessionKinds,
			applies: func(_ *Home, i *session.Instance) bool { return session.SupportsProjectSkills(i.Tool) }},
		{id: hotkeyOpenShellHere, label: "Shell", short: "Shell", kinds: sessionKinds},
		{id: hotkeyCopyOutput, label: "Copy", short: "Copy", kinds: sessionKinds},
		{id: footerPageBreak},
		{id: hotkeyExecShell, label: "Exec", short: "Exec", kinds: sessionKinds,
			applies: func(_ *Home, i *session.Instance) bool { return i.IsSandboxed() }},
		{id: hotkeyEditPaths, label: "Paths", short: "Paths", kinds: sessionKinds,
			applies: func(_ *Home, i *session.Instance) bool { return i.IsMultiRepo() }},
		{id: hotkeyCopyPane, label: "Copy pane", short: "Pane", kinds: sessionKinds},
		{id: hotkeyCopyInfo, label: "Copy ID/path", short: "Info", kinds: sessionKinds},
		{id: hotkeySendOutput, label: "Send", short: "Send", kinds: sessionKinds},
		{id: hotkeyEditNotes, label: "Notes", short: "Notes", kinds: sessionKinds,
			applies: func(*Home, *session.Instance) bool {
				cfg, _ := session.LoadUserConfig()
				return cfg == nil || cfg.GetShowNotes()
			}},
		{id: hotkeyAskAida, label: "Ask Aida", short: "Aida", kinds: sessionKinds},
		{id: hotkeyRename, label: "Rename", short: "Rename", kinds: bothKinds},
		{id: hotkeyMoveToGroup, label: "Move", short: "Move", kinds: sessionKinds},
		{id: hotkeyArchiveSession, label: "Archive", short: "Archive", kinds: sessionKinds},
		{id: hotkeyCloseSession, label: "Close", short: "Close", kinds: sessionKinds},
		{id: hotkeyDelete, label: "Delete", short: "Del", kinds: bothKinds},
	}
}

func footerCatalogByID() map[string]footerAction {
	m := make(map[string]footerAction)
	for _, a := range footerCatalog() {
		if a.id != footerPageBreak {
			m[a.id] = a
		}
	}
	return m
}

func (a footerAction) hasKind(kind string) bool {
	for _, k := range a.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func (a footerAction) keyFor(h *Home) string {
	if a.key != nil {
		return a.key(h)
	}
	return h.actionKey(a.id)
}

func (a footerAction) triggerFor(h *Home) string {
	if a.trigger != nil {
		return a.trigger(h)
	}
	return h.actionKey(a.id)
}

// defaultFooterLayout is the catalog order filtered to one row kind, with the
// page break kept where the catalog puts it. A group has few actions, so its
// default keeps them all on page 1.
func defaultFooterLayout(kind string) []string {
	var out []string
	for _, a := range footerCatalog() {
		if a.id == footerPageBreak {
			if kind == footerKindSession {
				out = append(out, footerPageBreak)
			}
			continue
		}
		if a.hasKind(kind) {
			out = append(out, a.id)
		}
	}
	if kind != footerKindSession {
		out = append(out, footerPageBreak)
	}
	return out
}

// normalizeFooterLayout cleans a saved layout: unknown or misplaced IDs are
// dropped, duplicates keep their first position, exactly one page break is
// kept, and catalog actions the layout does not mention (new in this build)
// are appended to page 2 in catalog order.
func normalizeFooterLayout(saved []string, kind string) []string {
	if len(saved) == 0 {
		return defaultFooterLayout(kind)
	}
	byID := footerCatalogByID()
	seen := map[string]bool{}
	var out []string
	breakSeen := false
	for _, id := range saved {
		id = strings.TrimSpace(id)
		if id == footerPageBreak {
			if !breakSeen {
				breakSeen = true
				out = append(out, footerPageBreak)
			}
			continue
		}
		a, ok := byID[id]
		if !ok || !a.hasKind(kind) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if !breakSeen {
		out = append(out, footerPageBreak)
	}
	for _, id := range defaultFooterLayout(kind) {
		if id != footerPageBreak && !seen[id] {
			out = append(out, id)
		}
	}
	return out
}

// footerLayout returns the effective layout for a row kind.
func (h *Home) footerLayout(kind string) []string {
	cfg, _ := session.LoadUserConfig()
	var saved []string
	if cfg != nil {
		if kind == footerKindGroup {
			saved = cfg.UI.FooterGroup
		} else {
			saved = cfg.UI.FooterSession
		}
	}
	return normalizeFooterLayout(saved, kind)
}

// persistFooterLayout writes one row kind's layout straight to config.toml
// (same pattern as patch 38's collapsed sections). nil restores the default.
func (h *Home) persistFooterLayout(kind string, layout []string) {
	cfg, err := session.LoadUserConfig()
	if err != nil || cfg == nil {
		return
	}
	if kind == footerKindGroup {
		cfg.UI.FooterGroup = layout
	} else {
		cfg.UI.FooterSession = layout
	}
	_ = session.SaveUserConfig(cfg)
}

// footerEntry is one resolved slot: an action with its key, label and state
// for the selected row.
type footerEntry struct {
	action footerAction
	key    string
	label  string
	ready  bool
	fits   bool // applies to this row (false: shown only in the menu, dimmed)
}

// footerRowKind returns the selected row's kind and session ("" when the list
// is empty or the cursor is out of range).
func (h *Home) footerRowKind() (string, *session.Instance) {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) {
		return "", nil
	}
	item := h.flatItems[h.cursor]
	if item.Type == session.ItemTypeGroup {
		return footerKindGroup, nil
	}
	if item.Session == nil {
		return "", nil
	}
	return footerKindSession, item.Session
}

// footerEntries resolves a layout for the selected row into its two pages.
// Unbound actions are left out of both; actions that do not apply to this row
// are left out unless includeAll (the menu shows them, dimmed).
func (h *Home) footerEntries(kind string, inst *session.Instance, layout []string, includeAll bool) (page1, page2 []footerEntry) {
	byID := footerCatalogByID()
	onPage2 := false
	for _, id := range layout {
		if id == footerPageBreak {
			onPage2 = true
			continue
		}
		a, ok := byID[id]
		if !ok {
			continue
		}
		key := a.keyFor(h)
		if key == "" {
			continue
		}
		e := footerEntry{action: a, key: key, label: a.label, ready: true, fits: true}
		if a.id == hotkeyTogglePreview {
			e.label = h.previewModeShort()
		}
		if inst != nil {
			if a.applies != nil && !a.applies(h, inst) {
				e.fits = false
			}
			if a.ready != nil && !a.ready(inst) {
				e.ready = false
			}
		}
		if !e.fits && !includeAll {
			continue
		}
		if onPage2 {
			page2 = append(page2, e)
		} else {
			page1 = append(page1, e)
		}
	}
	return page1, page2
}

// renderFooterChip renders one slot. wide selects the full-tier chip (padded)
// and labels; a dimmed slot keeps its width so nothing around it moves.
func (h *Home) renderFooterChip(e footerEntry, wide, longLabels bool) string {
	label := e.action.short
	if longLabels {
		label = e.action.label
	}
	if e.action.id == hotkeyTogglePreview {
		label = h.previewModeShort()
	}
	keyStyle := lipgloss.NewStyle().Foreground(ColorBg).Background(ColorAccent).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(ColorText)
	if !e.ready {
		keyStyle = lipgloss.NewStyle().Foreground(ColorComment).Background(ColorBorder)
		descStyle = lipgloss.NewStyle().Foreground(ColorComment)
	}
	if wide {
		keyStyle = keyStyle.Padding(0, 1)
	}
	return keyStyle.Render(e.key) + " " + descStyle.Render(label)
}

// toggleFooterPage flips the paged footer between page 1 and page 2.
func (h *Home) toggleFooterPage() {
	if h.footerPage == 0 {
		h.footerPage = 1
	} else {
		h.footerPage = 0
	}
}

// renderHelpBarPaged renders the "paged" footer: the current page's slots on
// the left (page-1 slots that do not fit move to the front of page 2), the
// page indicator, menu and help keys on the right.
func (h *Home) renderHelpBarPaged() string {
	if h.jumpMode {
		return h.renderHelpBarWidthAdaptive()
	}
	wide := h.width > 100
	longLabels := h.width >= 160
	sepStyle := lipgloss.NewStyle().Foreground(ColorBorder)
	sep := sepStyle.Render(" │ ")
	gap := " "
	globalStyle := lipgloss.NewStyle().Foreground(ColorComment)
	ctxStyle := lipgloss.NewStyle().Foreground(ColorPurple).Bold(true)

	kind, inst := h.footerRowKind()
	var title string
	var page1, page2 []footerEntry
	switch {
	case len(h.flatItems) == 0:
		title = "Empty"
		for _, a := range []struct{ id, label string }{
			{hotkeyNewSession, "New"}, {hotkeyImport, "Import"}, {hotkeyCreateGroup, "Group"},
		} {
			if k := h.actionKey(a.id); k != "" {
				page1 = append(page1, footerEntry{action: footerAction{id: a.id, label: a.label, short: a.label}, key: k, label: a.label, ready: true, fits: true})
			}
		}
	case kind == footerKindGroup:
		title = "Group"
		page1, page2 = h.footerEntries(kind, nil, h.footerLayout(kind), false)
	case kind == footerKindSession:
		title = "Session"
		page1, page2 = h.footerEntries(kind, inst, h.footerLayout(kind), false)
	}
	if len(h.undoStack) > 0 {
		if k := h.actionKey(hotkeyUndoDelete); k != "" {
			undo := footerEntry{action: footerAction{id: hotkeyUndoDelete, label: "Undo", short: "Undo"}, key: k, label: "Undo", ready: true, fits: true}
			page1 = append([]footerEntry{undo}, page1...)
		}
	}

	prefix := ctxStyle.Render(title + ":")
	render := func(entries []footerEntry) []string {
		out := make([]string, len(entries))
		for i, e := range entries {
			out[i] = h.renderFooterChip(e, wide, longLabels)
		}
		return out
	}

	// Right block: the page indicator, the menu key and Quit are pinned; the
	// rest drop from the end.
	buildRight := func(pages, n int) string {
		var parts []string
		if pages > 1 {
			if k := h.actionKey(hotkeyFooterPage); k != "" {
				parts = append(parts, globalStyle.Render(k+" Page "+string(rune('1'+h.footerPage))+"/2"))
			}
		}
		if k := h.actionKey(hotkeyFooterMenu); k != "" {
			parts = append(parts, globalStyle.Render(k+" All"))
		}
		var droppable []string
		if k := h.actionKey(hotkeyHelp); k != "" {
			droppable = append(droppable, globalStyle.Render(k+" Help"))
		}
		droppable = append(droppable, globalStyle.Render("↑↓ Nav"))
		if k := h.actionKey(hotkeySearch); k != "" {
			droppable = append(droppable, globalStyle.Render(k+" Search"))
		}
		if k := h.actionKey(hotkeySettings); k != "" {
			droppable = append(droppable, globalStyle.Render(k+" Settings"))
		}
		n = min(n, len(droppable))
		parts = append(parts, droppable[:n]...)
		if k := h.actionKey(hotkeyQuit); k != "" {
			parts = append(parts, globalStyle.Render(k+" Quit"))
		}
		return strings.Join(parts, sep)
	}
	const maxDroppable = 4

	// fitLeft keeps as many chips as fit beside right; the rest spill over.
	fitLeft := func(chips []string, right string, moreMark bool) (string, int) {
		budget := h.width - lipgloss.Width(right) - spacingNormal
		line := prefix
		kept := 0
		for i, c := range chips {
			candidate := line + gap + c
			tail := 0
			if moreMark && i < len(chips)-1 {
				tail = 2 // " …"
			}
			if lipgloss.Width(candidate)+tail > budget {
				break
			}
			line = candidate
			kept++
		}
		if moreMark && kept < len(chips) {
			line += " …"
		}
		return line, kept
	}

	chips1 := render(page1)
	pages := 1
	if len(page2) > 0 {
		pages = 2
	}
	// Choose the right block: as many droppable hints as leave page 1 intact,
	// else the bare pinned block.
	right := buildRight(pages, 0)
	for n := maxDroppable; n >= 0; n-- {
		r := buildRight(pages, n)
		if _, kept := fitLeft(chips1, r, false); kept == len(chips1) || n == 0 {
			right = r
			break
		}
	}
	_, kept1 := fitLeft(chips1, right, false)
	if kept1 < len(chips1) && pages == 1 {
		pages = 2
		right = buildRight(pages, 0)
		for n := maxDroppable; n >= 0; n-- {
			r := buildRight(pages, n)
			if _, k := fitLeft(chips1, r, false); k == kept1 || n == 0 {
				right = r
				break
			}
		}
		_, kept1 = fitLeft(chips1, right, false)
	}
	if pages == 1 {
		h.footerPage = 0
	}

	var left string
	if h.footerPage == 0 {
		left, _ = fitLeft(chips1, right, false)
	} else {
		spill := append(append([]footerEntry{}, page1[kept1:]...), page2...)
		left, _ = fitLeft(render(spill), right, true)
	}
	padding := max(spacingNormal, h.width-lipgloss.Width(left)-lipgloss.Width(right))
	content := left + strings.Repeat(" ", padding) + right

	border := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("─", max(0, h.width)))
	raw := lipgloss.JoinVertical(lipgloss.Left, border, content)
	return lipgloss.NewStyle().MaxWidth(h.width).Render(raw)
}

// ─── footer menu (":") ──────────────────────────────────────────────────────

// footerMenuState is the ":" menu: one row kind's layout being viewed or
// edited, with the cursor on an action (never on the page break).
type footerMenuState struct {
	open   bool
	kind   string
	layout []string
	cursor int
	dirty  bool
	// rowKind is the kind of the row that was selected when the menu opened;
	// actions only run when the menu shows that kind.
	rowKind string
}

func (h *Home) openFooterMenu() {
	kind, _ := h.footerRowKind()
	if kind == "" {
		kind = footerKindSession
	}
	h.footerMenu = footerMenuState{open: true, kind: kind, rowKind: kind, layout: h.footerLayout(kind)}
	h.footerMenu.cursor = h.footerMenuStep(-1, 1)
}

// footerMenuStep returns the next action index from 'from' in direction dir,
// skipping the page break; it stays put at either end.
func (h *Home) footerMenuStep(from, dir int) int {
	l := h.footerMenu.layout
	for i := from + dir; i >= 0 && i < len(l); i += dir {
		if l[i] != footerPageBreak {
			return i
		}
	}
	if from < 0 {
		return 0
	}
	return from
}

func (h *Home) closeFooterMenu() {
	if h.footerMenu.dirty {
		h.persistFooterLayout(h.footerMenu.kind, append([]string(nil), h.footerMenu.layout...))
	}
	h.footerMenu = footerMenuState{}
}

// footerMenuMove swaps the selected action with its neighbour in dir. Moving
// past the page break moves the action to the other page.
func (h *Home) footerMenuMove(dir int) {
	m := &h.footerMenu
	j := m.cursor + dir
	if j < 0 || j >= len(m.layout) {
		return
	}
	m.layout[m.cursor], m.layout[j] = m.layout[j], m.layout[m.cursor]
	m.cursor = j
	m.dirty = true
}

// footerMenuSwapPage sends the selected action to the start of page 2 (from
// page 1) or the end of page 1 (from page 2).
func (h *Home) footerMenuSwapPage() {
	m := &h.footerMenu
	bi := -1
	for i, v := range m.layout {
		if v == footerPageBreak {
			bi = i
			break
		}
	}
	if bi < 0 || m.cursor < 0 || m.cursor >= len(m.layout) || m.cursor == bi {
		return
	}
	id := m.layout[m.cursor]
	rest := append(append([]string{}, m.layout[:m.cursor]...), m.layout[m.cursor+1:]...)
	// From page 1 the break slid up one, so index bi is right after it; from
	// page 2 the break did not move, so index bi is right before it.
	at := bi
	out := append(append(append([]string{}, rest[:at]...), id), rest[at:]...)
	m.layout = out
	m.cursor = at
	m.dirty = true
}

func (h *Home) handleFooterMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := &h.footerMenu
	switch msg.String() {
	case "esc", "q", h.actionKey(hotkeyFooterMenu):
		h.closeFooterMenu()
	case "up", "k":
		m.cursor = h.footerMenuStep(m.cursor, -1)
	case "down", "j":
		m.cursor = h.footerMenuStep(m.cursor, 1)
	case "shift+up", "K":
		h.footerMenuMove(-1)
	case "shift+down", "J":
		h.footerMenuMove(1)
	case " ", "p":
		h.footerMenuSwapPage()
	case "tab":
		next := footerKindGroup
		if m.kind == footerKindGroup {
			next = footerKindSession
		}
		if m.dirty {
			h.persistFooterLayout(m.kind, append([]string(nil), m.layout...))
		}
		row := m.rowKind
		*m = footerMenuState{open: true, kind: next, rowKind: row, layout: h.footerLayout(next)}
		m.cursor = h.footerMenuStep(-1, 1)
	case "R":
		m.layout = defaultFooterLayout(m.kind)
		m.cursor = h.footerMenuStep(-1, 1)
		h.persistFooterLayout(m.kind, nil)
		m.dirty = false
	case "enter":
		if m.kind != m.rowKind || m.cursor < 0 || m.cursor >= len(m.layout) {
			return h, nil
		}
		a, ok := footerCatalogByID()[m.layout[m.cursor]]
		if !ok {
			return h, nil
		}
		trigger := a.triggerFor(h)
		h.closeFooterMenu()
		if trigger == "" {
			return h, nil
		}
		return h.handleMainKey(footerKeyMsg(trigger))
	}
	return h, nil
}

// footerKeyMsg turns a bound key string back into the KeyMsg that produces it.
func footerKeyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case " ", "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	if strings.HasPrefix(s, "ctrl+") && len(s) == len("ctrl+")+1 {
		c := s[len(s)-1]
		if c >= 'a' && c <= 'z' {
			return tea.KeyMsg{Type: tea.KeyCtrlA + tea.KeyType(c-'a')}
		}
	}
	if strings.HasPrefix(s, "alt+") && len([]rune(s)) == len("alt+")+1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s[len("alt+"):]), Alt: true}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// renderFooterMenu draws the ":" menu full-screen.
func (h *Home) renderFooterMenu() string {
	m := h.footerMenu
	titleStyle := lipgloss.NewStyle().Foreground(ColorPurple).Bold(true)
	headStyle := lipgloss.NewStyle().Foreground(ColorComment)
	dim := lipgloss.NewStyle().Foreground(ColorComment)
	text := lipgloss.NewStyle().Foreground(ColorText)
	sel := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(ColorBg).Background(ColorAccent).Bold(true).Padding(0, 1)
	keyDim := lipgloss.NewStyle().Foreground(ColorComment).Background(ColorBorder).Padding(0, 1)

	var inst *session.Instance
	if m.kind == m.rowKind {
		_, inst = h.footerRowKind()
	}
	byID := footerCatalogByID()

	kindName := "Session"
	if m.kind == footerKindGroup {
		kindName = "Group"
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("  All actions — " + kindName + " rows"))
	if m.kind != m.rowKind {
		b.WriteString(dim.Render("   (arranging only: select a " + strings.ToLower(kindName) + " row to run these)"))
	}
	b.WriteString("\n\n")
	body := []string{headStyle.Render("  ── Page 1: always on the bar ──")}
	cursorLine := 0
	keyW := 0
	for _, id := range m.layout {
		if a, ok := byID[id]; ok {
			keyW = max(keyW, lipgloss.Width(a.keyFor(h)))
		}
	}
	for i, id := range m.layout {
		if id == footerPageBreak {
			pk := h.actionKey(hotkeyFooterPage)
			body = append(body, "", headStyle.Render("  ── Page 2: press "+pk+" on the bar to show ──"))
			continue
		}
		a, ok := byID[id]
		if !ok {
			continue
		}
		k := a.keyFor(h)
		note := ""
		usable := true
		if k == "" {
			k = "—"
			note = "unbound"
			usable = false
		}
		if inst != nil {
			if a.applies != nil && !a.applies(h, inst) {
				note = "not for this session"
				usable = false
			} else if a.ready != nil && !a.ready(inst) {
				note = "not available right now"
				usable = false
			}
		}
		label := a.label
		if a.id == hotkeyTogglePreview {
			label = "Preview mode (" + h.previewModeShort() + ")"
		}
		marker := "   "
		ls := text
		if i == m.cursor {
			marker = sel.Render(" ▸ ")
			ls = sel
		}
		ks := keyStyle
		if !usable {
			ks = keyDim
			if i != m.cursor {
				ls = dim
			}
		}
		chip := ks.Render(k) + strings.Repeat(" ", max(0, keyW-lipgloss.Width(k)))
		line := marker + chip + "  " + ls.Render(label)
		if note != "" {
			line += dim.Render("  (" + note + ")")
		}
		if i == m.cursor {
			cursorLine = len(body)
		}
		body = append(body, line)
	}
	// Window the body around the cursor when the terminal is short: the title
	// takes 2 rows and the key guide 3.
	room := max(3, h.height-5)
	if len(body) > room {
		top := min(max(0, cursorLine-room/2), len(body)-room)
		body = body[top : top+room]
	}
	b.WriteString(strings.Join(body, "\n"))
	b.WriteString("\n\n")
	b.WriteString(dim.Render("  ↑↓ select · Enter run · Space send to the other page · Shift+↑↓ (K/J) reorder"))
	b.WriteString("\n")
	guide := "  Tab session/group rows · R reset to default · Esc close"
	if m.dirty {
		guide += " (saves your changes)"
	}
	b.WriteString(dim.Render(guide))
	return clampViewToViewport(b.String(), h.width, h.height)
}
