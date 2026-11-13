package tui

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/view"
)

const (
	// boardFullEvery is how often the board reads everything again,
	// including which plan each pin names and the plan's usual build time.
	boardFullEvery = 60 * time.Second
	// boardSamples is how many points the two small charts keep.
	boardSamples = 240
	// boardMinRows and boardMinCols are the terminal size under which the
	// board shows its header and Needs you only.
	boardMinRows, boardMinCols = 20, 60
)

// boardEvery is the board's beat: agents, the queue and running builds are
// read again on each. A variable only so tests can make the beat fire at once.
var boardEvery = 15 * time.Second

// boardRedrawEvery is the board's redraw beat: it only draws the screen
// again, so the data age and queue waits count up between reads. It never
// reads the server. A variable only so tests can make it fire at once.
var boardRedrawEvery = time.Second

type boardPanel int

const (
	boardNeeds boardPanel = iota
	boardQueue
	boardPins
)

// boardState is the board screen. Nothing in it is saved: the charts cover
// the time since the board opened.
type boardState struct {
	data    app.Board
	have    bool
	stale   bool
	loading bool
	// loadedAt is the clock time of the last read that worked; fullAt the
	// last full one.
	loadedAt, fullAt time.Time
	tickGen, loadGen int
	panel            boardPanel
	cursor           [3]int
	groups           []app.PinGroup
	// busy and queued are the chart samples, oldest first.
	busy, queued []float64
	// seen holds the Needs you entries already shown, so the bell rings only
	// for one that is new. firstSeen is when a queued build was first seen,
	// for builds whose own record gives no time they began waiting.
	seen      map[string]bool
	firstSeen map[string]time.Time
	pinFor    string // the plan the group prompt will pin
}

type boardTickMsg struct{ Gen int }

// boardRedrawMsg is one redraw beat; it shares the read beat's generation,
// so both loops end together.
type boardRedrawMsg struct{ Gen int }

type boardLoadedMsg struct {
	Gen    int
	Full   bool
	Board  app.Board
	Groups []app.PinGroup
	Err    error
}

func boardTickCmd(gen int) tea.Cmd {
	return tea.Tick(boardEvery, func(time.Time) tea.Msg { return boardTickMsg{Gen: gen} })
}

func boardRedrawCmd(gen int) tea.Cmd {
	return tea.Tick(boardRedrawEvery, func(time.Time) tea.Msg { return boardRedrawMsg{Gen: gen} })
}

// boardBeats starts both of the board's loops for one generation.
func boardBeats(gen int) tea.Cmd {
	return tea.Batch(boardTickCmd(gen), boardRedrawCmd(gen))
}

// boardRedraw is one redraw beat: returning from Update is all a redraw
// needs. A beat off the board or of an older generation ends the loop.
func (m Model) boardRedraw(msg boardRedrawMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.board.tickGen || m.screen != screenBoard {
		return m, nil
	}
	return m, boardRedrawCmd(m.board.tickGen)
}

// showBoard opens the board, and reads it at once. It starts one beat loop;
// a loop is ended by bumping the generation, so there are never two.
func (m Model) showBoard() (tea.Model, tea.Cmd) {
	if m.screen == screenPlan {
		m.closePlan()
	}
	m.screen = screenBoard
	m.fromSet, m.fromTree, m.logsFromTree, m.fromBoard = false, false, false, false
	m.board.tickGen++
	load := m.loadBoard(true)
	return m, tea.Batch(load, boardBeats(m.board.tickGen))
}

// loadBoard reads the board. full also reads each pin's plan and usual time;
// a quick read keeps them from the board on screen.
func (m *Model) loadBoard(full bool) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	m.board.loadGen++
	m.board.loading = true
	var prev *app.Board
	if !full && m.board.have {
		p := m.board.data
		prev = &p
	}
	return m.readBoardCmd(full, prev, m.board.loadGen)
}

// readBoardCmd is the read itself. Init uses it directly: its receiver is a
// copy, so a generation bumped there would be lost.
func (m Model) readBoardCmd(full bool, prev *app.Board, gen int) tea.Cmd {
	svc, ctx, monitor := m.svc, m.baseCtx(), m.deps.Monitor
	return func() tea.Msg {
		var groups []app.PinGroup
		if monitor != nil {
			var err error
			if groups, err = monitor(); err != nil {
				return boardLoadedMsg{Gen: gen, Full: full, Err: err}
			}
		}
		b, err := svc.RefreshBoard(ctx, groups, prev)
		return boardLoadedMsg{Gen: gen, Full: full, Board: b, Groups: groups, Err: err}
	}
}

// boardTick is one beat. Unlike Home's, it never stops for idleness: a board
// nobody touches is the board in use. A beat off the board ends the loop.
func (m Model) boardTick(msg boardTickMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.board.tickGen || m.screen != screenBoard {
		return m, nil
	}
	next := boardTickCmd(m.board.tickGen)
	if m.board.loading {
		return m, next
	}
	full := m.now().Sub(m.board.fullAt) >= boardFullEvery
	return m, tea.Batch(m.loadBoard(full), next)
}

// boardLoaded folds a read into the board. A read that got nothing leaves
// what is on screen and marks it stale.
func (m Model) boardLoaded(msg boardLoadedMsg) Model {
	if msg.Gen != m.board.loadGen {
		return m
	}
	b := &m.board
	b.loading = false
	if msg.Err != nil || readNothing(msg.Board) {
		b.stale = true
		if msg.Err != nil {
			m.err = msg.Err
		}
		return m
	}
	now := m.now()
	b.stale = false
	b.loadedAt = now
	if msg.Full {
		b.fullAt = now
	}
	b.groups = msg.Groups
	data := msg.Board
	// A build whose record gave no time it began waiting counts from when
	// the board first saw it, and keeps counting across reads.
	seen := map[string]time.Time{}
	for i, r := range data.Queue {
		if !r.AtLeast {
			continue
		}
		t, ok := b.firstSeen[r.Key]
		if !ok {
			t = now
		}
		seen[r.Key] = t
		data.Queue[i].Waited = max(now.Sub(t), 0)
	}
	b.firstSeen = seen
	data.Needs = app.NeedsYou(data, now)

	if a := data.Agents; a != nil && a.Online > 0 {
		b.busy = appendSample(b.busy, float64(a.Busy)/float64(a.Online))
	}
	if data.QueueOK {
		b.queued = appendSample(b.queued, float64(len(data.Queue)))
	}
	fresh := false
	next := map[string]bool{}
	for _, n := range data.Needs {
		id := needID(n)
		next[id] = true
		if b.have && !b.seen[id] {
			fresh = true
		}
	}
	b.seen, b.data, b.have = next, data, true
	b.clamp()
	if fresh {
		m.ringBell()
	}
	return m
}

func needID(n app.Need) string {
	if n.Build != "" {
		return n.Kind + "|" + n.Build
	}
	return n.Kind + "|" + n.Text
}

func appendSample(s []float64, v float64) []float64 {
	s = append(s, v)
	if len(s) > boardSamples {
		s = s[len(s)-boardSamples:]
	}
	return s
}

// pinRows flattens the groups' pins in screen order.
func (b *boardState) pinRows() []app.PinView {
	var out []app.PinView
	for _, g := range b.data.Groups {
		out = append(out, g.Pins...)
	}
	return out
}

func (b *boardState) rows(p boardPanel) int {
	switch p {
	case boardNeeds:
		return len(b.data.Needs)
	case boardQueue:
		return len(b.data.Queue)
	}
	return len(b.pinRows())
}

func (b *boardState) clamp() {
	for p := range b.cursor {
		b.cursor[p] = max(min(b.cursor[p], b.rows(boardPanel(p))-1), 0)
	}
}

func (b *boardState) move(delta int) {
	b.cursor[b.panel] = b.cursor[b.panel] + delta
	b.clamp()
}

// readNothing says whether a read got nothing at all: no agents, no queue
// and every pin it tried failed. A read with no pins to try read them all.
func readNothing(d app.Board) bool {
	if d.Agents != nil || d.QueueOK {
		return false
	}
	tried := false
	for _, g := range d.Groups {
		for _, p := range g.Pins {
			if p.Err == nil {
				return false
			}
			tried = true
		}
	}
	return tried
}

// buildByKey is the build the board read with this key: queued, running
// or a pin's result.
func (b *boardState) buildByKey(key string) (provider.Build, bool) {
	for _, q := range b.data.Queue {
		if q.Key == key {
			return provider.Build{Key: q.Key, PlanKey: q.PlanKey, Number: q.Number, State: provider.StateQueued}, true
		}
	}
	for _, g := range b.data.Groups {
		for _, p := range g.Pins {
			if p.Current != nil && p.Current.Key == key {
				return *p.Current, true
			}
			for _, r := range p.Results {
				if r.Key == key {
					return r, true
				}
			}
		}
	}
	return provider.Build{}, false
}

// selectedBuild is the build under the cursor: a queued build, a pin's build
// running now or else its newest result, or the build a Needs you line names.
func (m Model) boardBuild() (provider.Build, bool) {
	b := &m.board
	c := b.cursor[b.panel]
	switch b.panel {
	case boardNeeds:
		if c < len(b.data.Needs) && b.data.Needs[c].Build != "" {
			return b.buildByKey(b.data.Needs[c].Build)
		}
	case boardQueue:
		if c < len(b.data.Queue) {
			return b.buildByKey(b.data.Queue[c].Key)
		}
	default:
		if rows := b.pinRows(); c < len(rows) {
			p := rows[c]
			switch {
			case p.Current != nil:
				return *p.Current, true
			case len(p.Results) > 0:
				return p.Results[0], true
			}
		}
	}
	return provider.Build{}, false
}

// boardPlan is the plan under the cursor of the pins panel.
func (m Model) boardPin() (app.PinView, bool) {
	rows := m.board.pinRows()
	c := m.board.cursor[boardPins]
	if m.board.panel != boardPins || c >= len(rows) {
		return app.PinView{}, false
	}
	return rows[c], true
}

// handleBoardKey owns the keys of the board screen. No key changes anything
// on the server; P writes bam's settings file only.
func (m Model) handleBoardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Back):
		return m, nil // the board is the top
	case key.Matches(msg, keys.NextPanel):
		m.board.panel = (m.board.panel + 1) % 3
	case key.Matches(msg, keys.PrevPanel):
		m.board.panel = (m.board.panel + 2) % 3
	case key.Matches(msg, keys.Down):
		m.board.move(1)
	case key.Matches(msg, keys.Up):
		m.board.move(-1)
	case key.Matches(msg, keys.Top):
		m.board.cursor[m.board.panel] = 0
	case key.Matches(msg, keys.Bottom):
		m.board.cursor[m.board.panel] = max(m.board.rows(m.board.panel)-1, 0)
	case key.Matches(msg, keys.Enter):
		return m.boardOpen(false)
	case key.Matches(msg, keys.Logs):
		return m.boardOpen(true)
	case key.Matches(msg, keys.Tree):
		if b, ok := m.boardBuild(); ok {
			return m.openTree(b.Key)
		}
	case key.Matches(msg, keys.Investigate):
		return m.boardInvestigate()
	case key.Matches(msg, keys.Open):
		return m.openSelection()
	case key.Matches(msg, keys.Copy):
		return m.copySelection()
	case key.Matches(msg, keys.Project):
		return m.boardPinKey()
	case key.Matches(msg, keys.Command):
		return m.startCommand()
	case key.Matches(msg, keys.Refresh):
		m.err = nil
		return m, m.loadBoard(true)
	case key.Matches(msg, keys.Server):
		return m.openServerPicker()
	case key.Matches(msg, keys.Help):
		return m.openHelp()
	case key.Matches(msg, keys.ExpandErr):
		if m.err != nil {
			m.overlay = overlayError
		}
	}
	return m, nil
}

// boardOpen opens the selection in the plan screen: a pin shows its plan, a
// build shows its stages. With logs it also takes the build's logs, as l
// does on the plan screen. esc comes back to the board.
func (m Model) boardOpen(logs bool) (tea.Model, tea.Cmd) {
	if m.svc == nil {
		return m, nil
	}
	if pin, isPin := m.boardPin(); isPin && !logs {
		if pin.PlanKey == "" {
			return m, nil
		}
		next, cmd := m.enterPlan(m.masterOf(pin.PlanKey), pin.PlanKey, nil, focusBuilds)
		next.fromBoard = true
		return next, cmd
	}
	b, ok := m.boardBuild()
	if !ok {
		return m, nil
	}
	next, cmd := m.enterPlan(m.masterOf(b.PlanKey), b.PlanKey, &b, focusStages)
	next.fromBoard = true
	if !logs {
		return next, cmd
	}
	var more tea.Model
	var logsCmd tea.Cmd
	if b.State.Finished() {
		more, logsCmd = next.openLogs("", false)
	} else {
		more, logsCmd = next.openStageLogs("")
	}
	return more, tea.Batch(cmd, logsCmd)
}

// boardInvestigate is i: why the selected pin's newest build failed.
func (m Model) boardInvestigate() (tea.Model, tea.Cmd) {
	b, ok := m.boardBuild()
	if !ok || m.svc == nil {
		return m, nil
	}
	if b.State != provider.StateFailed {
		m.status = b.Key + " did not fail; nothing to investigate"
		return m, nil
	}
	m.infoGen++
	m = m.openInfo("Why "+b.Key+" failed", "reading the failed jobs' logs…")
	width := overlayWidth(m.width, helpMaxWidth) - 2
	out := func(buf *bytes.Buffer) view.Out { return m.infoOut(buf, width) }
	return m, investigateCmd(m.baseCtx(), m.svc, b, out, m.infoGen)
}

// boardSelectionURL is the Bamboo URL for o and y on the board.
func (m Model) boardSelectionURL() string {
	if m.svc == nil {
		return ""
	}
	if pin, ok := m.boardPin(); ok && pin.PlanKey != "" {
		return m.svc.P.URL(pin.PlanKey)
	}
	if b, ok := m.boardBuild(); ok {
		return m.svc.P.URL(b.Key)
	}
	return ""
}

// boardPinKey is P: unpin the selected pipeline, or pin the selected queued
// build's plan after asking for the group.
func (m Model) boardPinKey() (tea.Model, tea.Cmd) {
	if pin, ok := m.boardPin(); ok {
		if m.deps.PinRemove == nil {
			return m, nil
		}
		remove, name := m.deps.PinRemove, pin.Pin
		return m, func() tea.Msg {
			if err := remove(name); err != nil {
				return errMsg{Err: err, Where: "pin"}
			}
			return boardPinnedMsg{Status: "unpinned " + name}
		}
	}
	if m.board.panel != boardQueue || m.deps.PinAdd == nil {
		return m, nil
	}
	b, ok := m.boardBuild()
	if !ok || b.PlanKey == "" {
		return m, nil
	}
	m.board.pinFor = b.PlanKey
	m.inputFor = inputPin
	m.input.SetValue("")
	m.input.Prompt = "pin " + b.PlanKey + " to group: "
	m.input.Focus()
	return m, textinput.Blink
}

// boardPinnedMsg says the settings file changed; the board reads it again.
type boardPinnedMsg struct{ Status string }

// pinTo writes the pin the group prompt asked about. An empty group pins
// nothing, as esc does.
func (m Model) pinTo(group string) (tea.Model, tea.Cmd) {
	if group == "" || m.deps.PinAdd == nil {
		return m, nil
	}
	add, plan := m.deps.PinAdd, m.board.pinFor
	return m, func() tea.Msg {
		if err := add(group, plan); err != nil {
			return errMsg{Err: err, Where: "pin"}
		}
		return boardPinnedMsg{Status: "pinned " + plan + " in " + group}
	}
}

// ---- drawing ----

// boardTooSmall says whether the terminal is under the size the whole board
// needs.
func (m Model) boardTooSmall() bool { return m.height < boardMinRows || m.width < boardMinCols }

func (m Model) boardHeader() string {
	b := &m.board
	left := m.server
	if m.info.Version != "" {
		left += " · Bamboo " + m.info.Version
	}
	var right string
	switch {
	case !b.have && b.loading:
		right = "reading…"
	case !b.have:
		right = "no data yet"
	default:
		right = "updated " + view.Ago(max(m.now().Sub(b.loadedAt), 0))
	}
	if b.stale {
		right = errorStyle.Render("stale · " + right)
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		return truncate(left+"  "+right, m.width)
	}
	return " " + titleStyle.Render(left) + strings.Repeat(" ", gap) + right
}

func (m Model) boardView() string {
	out := []string{m.boardHeader()}
	body := m.boardBody()
	out = append(out, body...)
	for len(out) < m.height-1 {
		out = append(out, "")
	}
	out = out[:min(len(out), m.height-1)]
	for i, l := range out {
		out[i] = truncate(l, m.width)
	}
	out = append(out, m.statusBar(m.width))
	return m.overlayView(strings.Join(out, "\n"))
}

func (m Model) boardBody() []string {
	b := &m.board
	h := m.height - 2 // header and status bar
	needs := m.needsLines()
	if m.boardTooSmall() {
		lines := strings.Split(panel("Needs you", b.panel == boardNeeds, m.width, min(len(needs)+2, h-1), strings.Join(needs, "\n")), "\n")
		return append(lines, dimStyle.Render(" more at 60 columns by 20 rows"))
	}
	inner := m.width - 2
	nh := min(len(needs), 4) + 2
	awayMax := 2
	if h >= 26 {
		awayMax = 4
	}
	ah := 4 + min(awayCount(b), awayMax)
	nh = max(min(nh, h-ah-7), 3)
	rest := h - nh - ah
	queue, queueCursor := m.queueLines(inner)
	qh := max(min(len(queue)+2, rest-4), 3)
	ph := rest - qh

	var lines []string
	add := func(title string, focused bool, height int, body []string) {
		for i, l := range body {
			body[i] = truncate(l, inner)
		}
		lines = append(lines, strings.Split(panel(title, focused, m.width, height, strings.Join(body, "\n")), "\n")...)
	}
	add("Needs you", b.panel == boardNeeds, nh, window(needs, b.cursor[boardNeeds], nh-2))
	add("Agents", false, ah, m.agentLines(inner, awayMax))
	add("Queue", b.panel == boardQueue, qh, window(queue, queueCursor, qh-2))
	pins, pinCursor := m.pinLines()
	add("Pinned", b.panel == boardPins, ph, window(pins, pinCursor, ph-2))
	return lines
}

func awayCount(b *boardState) int {
	if b.data.Agents == nil {
		return 0
	}
	return len(b.data.Agents.Away)
}

// window shows the rows that fit in h, scrolled so the cursor row is in view.
func window(lines []string, cursor, h int) []string {
	if h <= 0 || len(lines) <= h {
		return lines
	}
	start := max(min(cursor-h/2, len(lines)-h), 0)
	return lines[start : start+h]
}

func (m Model) needsLines() []string {
	b := &m.board
	if !b.have {
		return []string{dimStyle.Render("  reading the server…")}
	}
	if len(b.data.Needs) == 0 {
		return []string{dimStyle.Render("  Nothing needs you")}
	}
	var out []string
	for i, n := range b.data.Needs {
		out = append(out, m.cursorFor(b.panel == boardNeeds, i == b.cursor[boardNeeds])+errorStyle.Render("!")+" "+n.Text)
	}
	return out
}

func (m Model) agentLines(inner, awayMax int) []string {
	b := &m.board
	if !b.have {
		return []string{dimStyle.Render("  reading the agents…")}
	}
	a := b.data.Agents
	if a == nil {
		return []string{"  " + b.data.AgentsNote}
	}
	share := 0.0
	if a.Online > 0 {
		share = float64(a.Busy) / float64(a.Online)
	}
	sw := max(min((inner-32)/2, 24), 6)
	out := []string{
		fmt.Sprintf("  busy %s %d of %d online · %d offline · %d disabled", bar(share, 10), a.Busy, a.Online, a.Offline, a.Disabled),
		fmt.Sprintf("  busy %s  queued %s  since opened", spark(b.busy, 1, sw), spark(b.queued, 0, sw)),
	}
	shown := a.Away
	if len(shown) > awayMax {
		shown = shown[:awayMax-1]
	}
	for _, ag := range shown {
		why := "offline"
		if !ag.Enabled {
			why = "disabled"
		}
		out = append(out, fmt.Sprintf("  %s  %s  %s", ag.Name, strings.ToLower(ag.Type), why))
	}
	if more := len(a.Away) - len(shown); more > 0 {
		out = append(out, fmt.Sprintf("  … and %d more offline or disabled", more))
	}
	return out
}

// queueLines draws the queue, one line per build, and says which line holds
// the cursor. The columns are as wide as their widest value, and why a build
// waits comes before the trigger reason: when the line is too wide the
// reason is cut short with an ellipsis, then the agents' names go, then
// long keys are shortened, so the why stays readable.
func (m Model) queueLines(inner int) ([]string, int) {
	b := &m.board
	switch {
	case !b.have:
		return []string{dimStyle.Render("  reading the queue…")}, 0
	case !b.data.QueueOK:
		return []string{"  " + b.data.QueueNote}, 0
	case len(b.data.Queue) == 0:
		return []string{dimStyle.Render("  The queue is empty")}, 0
	}
	waits := make([]string, len(b.data.Queue))
	posW, keyW, waitW := 0, 0, 0
	for i, r := range b.data.Queue {
		// Waited is as of the last read; it keeps counting until the next.
		w := r.Waited + max(m.now().Sub(b.loadedAt), 0)
		waits[i] = brief(w)
		if w <= 0 {
			waits[i] = "0s"
		}
		if r.AtLeast {
			waits[i] = "at least " + waits[i]
		}
		posW = max(posW, len(fmt.Sprint(r.Position)))
		keyW = max(keyW, lipgloss.Width(r.Key))
		waitW = max(waitW, lipgloss.Width(waits[i]))
	}
	// A long key must not push a why past the panel: shorten the KEY
	// column, down to a floor, until the shortest form of every why fits.
	needW := 0
	for _, r := range b.data.Queue {
		if r.Why != nil {
			short := *r.Why
			short.Agents = nil
			needW = max(needW, lipgloss.Width(view.WhyText(&short)))
		}
	}
	if fit := inner - (2 + posW + 2 + 2 + waitW + 2) - needW; fit < keyW {
		keyW = max(fit, min(keyW, 10))
	}
	var out []string
	for i, r := range b.data.Queue {
		key := r.Key
		if lipgloss.Width(key) > keyW {
			key = ansi.Truncate(key, keyW, "…")
		}
		head := fmt.Sprintf("%s%-*d  %-*s  %-*s  ", m.cursorFor(b.panel == boardQueue, i == b.cursor[boardQueue]), posW, r.Position, keyW, key, waitW, waits[i])
		room := inner - lipgloss.Width(head)
		why := view.WhyText(r.Why)
		if r.Why != nil && len(r.Why.Agents) > 0 && lipgloss.Width(why) > room {
			short := *r.Why
			short.Agents = nil
			why = view.WhyText(&short)
		}
		if lipgloss.Width(why) > room {
			why = ansi.Truncate(why, max(room, 1), "…")
		}
		text := r.Reason
		if why != "" {
			text = why
			if rest := room - lipgloss.Width(why+" · "); r.Reason != "" && rest > 1 {
				text += " · " + ansi.Truncate(r.Reason, rest, "…")
			}
		}
		out = append(out, head+dimStyle.Render(text))
	}
	return out, b.cursor[boardQueue]
}

// pinLines draws the groups, and says which line holds the cursor.
func (m Model) pinLines() ([]string, int) {
	b := &m.board
	if !b.have {
		return []string{dimStyle.Render("  reading the pipelines…")}, 0
	}
	if len(b.data.Groups) == 0 {
		return []string{dimStyle.Render("  No pipelines pinned. Pin one with: bam pin add GROUP PLAN")}, 0
	}
	var out []string
	cursorLine, n := 0, 0
	for _, g := range b.data.Groups {
		out = append(out, "  "+titleStyle.Render(g.Name))
		for _, p := range g.Pins {
			sel := b.panel == boardPins && n == b.cursor[boardPins]
			if sel {
				cursorLine = len(out)
			}
			out = append(out, m.cursorFor(b.panel == boardPins, n == b.cursor[boardPins])+m.pinText(p))
			n++
		}
	}
	return out, cursorLine
}

func (m Model) pinText(p app.PinView) string {
	if p.Err != nil {
		return fmt.Sprintf("%-22s %s", p.Pin, errorStyle.Render("could not be read: "+p.Err.Error()))
	}
	var sb strings.Builder
	for i := len(p.Results) - 1; i >= 0; i-- {
		sb.WriteString(stateStyle(p.Results[i].State).Render(stateGlyph(p.Results[i].State)))
	}
	now := "–"
	if c := p.Current; c != nil {
		now = fmt.Sprintf("#%d %s", c.Number, strings.ToLower(string(c.State)))
		if c.State == provider.StateRunning && p.Usual > 0 && !c.StartedAt.IsZero() {
			run := max(m.now().Sub(c.StartedAt), 0)
			now += fmt.Sprintf(" %s %s/%s", bar(float64(run)/float64(p.Usual), 6), brief(run), brief(p.Usual))
		}
	}
	return fmt.Sprintf("%-14s %s %-24s last %s · usual %s", truncate(p.Pin, 14), results(sb.String(), len(p.Results)), now, brief(p.Last), brief(p.Usual))
}

// bar draws frac (0 to 1, more is full) as w cells.
func bar(frac float64, w int) string {
	frac = max(min(frac, 1), 0)
	n := int(frac*float64(w) + 0.5)
	return strings.Repeat("█", n) + strings.Repeat("░", w-n)
}

// spark draws the last w samples as one row of blocks. A ceiling of 0 scales
// to the largest sample; otherwise samples are fractions of the ceiling.
func spark(s []float64, ceiling float64, w int) string {
	if len(s) == 0 {
		return strings.Repeat("·", w)
	}
	if len(s) > w {
		s = s[len(s)-w:]
	}
	if ceiling == 0 {
		for _, v := range s {
			ceiling = max(ceiling, v)
		}
		if ceiling == 0 {
			ceiling = 1
		}
	}
	const levels = "▁▂▃▄▅▆▇█"
	rs := []rune(levels)
	var sb strings.Builder
	for _, v := range s {
		i := int(max(min(v/ceiling, 1), 0) * float64(len(rs)-1))
		sb.WriteRune(rs[i])
	}
	return sb.String() + strings.Repeat(" ", w-len(s))
}

// results pads the glyphs of a pin's last builds to the ten the column holds.
func results(glyphs string, n int) string {
	return glyphs + strings.Repeat(" ", max(app.PinResults-n, 0))
}

// brief is a duration for a small column: whole seconds under a minute,
// whole minutes under an hour, then hours and minutes. Zero is a dash.
func brief(d time.Duration) string {
	switch {
	case d <= 0:
		return "–"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}
