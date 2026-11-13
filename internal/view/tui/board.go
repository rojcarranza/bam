package tui

import (
	"bytes"
	"fmt"
	"slices"
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
	boardAgents // a tab stop only while Your agents is shown
	boardPanels
)

// boardOrder is the panels in tab order: Your agents sits after Needs you
// when it is shown, and is skipped otherwise.
func (b *boardState) boardOrder() []boardPanel {
	if b.data.Mine != nil {
		return []boardPanel{boardNeeds, boardAgents, boardQueue, boardPins}
	}
	return []boardPanel{boardNeeds, boardQueue, boardPins}
}

// tab moves the focus delta panels along the tab order.
func (b *boardState) tab(delta int) {
	order := b.boardOrder()
	i := slices.Index(order, b.panel)
	if i < 0 {
		i = 0
	}
	b.panel = order[(i+delta+len(order))%len(order)]
}

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
	cursor           [boardPanels]int
	// whole is true while the board shows the whole server, not only your
	// agents: bam top --all, and the a key.
	whole  bool
	groups []app.PinGroup
	// busy and queued are the chart samples, oldest first.
	busy, queued []float64
	// seen holds the Needs you entries already shown, so the bell rings only
	// for one that is new.
	seen   map[string]bool
	pinFor string // the plan the group prompt will pin
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
	if m.board.whole {
		ctx = app.WholeServer(ctx)
	}
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
	data.Needs = app.NeedsYou(data, now)

	if a := data.Agents; a != nil && a.Online > 0 {
		b.busy = appendSample(b.busy, float64(a.Busy)/float64(a.Online))
	}
	if data.QueueOK {
		b.queued = appendSample(b.queued, float64(len(data.Queue)+data.QueueOthers))
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
	case boardAgents:
		if b.data.Mine == nil {
			return 0
		}
		return len(b.data.Mine.Pools)
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
		m.board.tab(1)
	case key.Matches(msg, keys.PrevPanel):
		m.board.tab(-1)
	case key.Matches(msg, keys.Down):
		m.board.move(1)
	case key.Matches(msg, keys.Up):
		m.board.move(-1)
	case key.Matches(msg, keys.Top):
		m.board.cursor[m.board.panel] = 0
	case key.Matches(msg, keys.Bottom):
		m.board.cursor[m.board.panel] = max(m.board.rows(m.board.panel)-1, 0)
	case key.Matches(msg, keys.AllLogs):
		return m.boardToggleWhole()
	case key.Matches(msg, keys.Enter):
		if m.board.panel == boardAgents {
			return m.boardPoolInfo()
		}
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

// startPinPlan is m on the plans table and on a plan's screen: it asks for
// the group and pins the selected plan, as P does for a queued build.
func (m Model) startPinPlan() (tea.Model, tea.Cmd) {
	if m.deps.PinAdd == nil {
		return m, nil
	}
	plan := ""
	switch {
	case m.screen == screenHome && m.home.view == homePlans:
		if p, ok := m.plans.selected(); ok {
			plan = p.Key
		}
	case m.screen == screenPlan:
		plan = m.plan.key
	}
	if plan == "" {
		return m, nil
	}
	m.board.pinFor = plan
	m.inputFor = inputPin
	m.input.SetValue("")
	m.input.Prompt = "pin " + plan + " to group: "
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
	needs := m.needsLines(m.width - 2)
	if m.boardTooSmall() {
		lines := strings.Split(panel("Needs you", b.panel == boardNeeds, m.width, min(len(needs)+2, h-1), strings.Join(needs, "\n")), "\n")
		return append(lines, dimStyle.Render(" more at 60 columns by 20 rows"))
	}
	if m.height >= roomyRows {
		return m.roomyBoardBody()
	}
	inner := m.width - 2
	nh := min(len(needs), 4) + 2
	awayMax := 2
	if h >= 26 {
		awayMax = 4
	}
	ah := 4 + min(awayCount(b), awayMax)
	switch {
	case b.data.Mine != nil:
		ah = min(len(m.mineLines())+2, 4+awayMax)
	case m.hintMine():
		ah++ // the hint line
	}
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
	agents := m.agentLines(inner, awayMax)
	if b.data.Mine != nil {
		agents = fitMine(agents, b.data.Mine, ah-2)
	}
	add(m.agentsTitle(), b.panel == boardAgents, ah, window(agents, m.agentCursorLine(), ah-2))
	add(m.queueTitle(), b.panel == boardQueue, qh, window(queue, queueCursor, qh-2))
	pins, pinCursor := m.pinLines(inner, false)
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

// needsLines is Needs you's lines for a panel w columns wide inside; a line
// too long for it ends in an ellipsis, as queue rows do.
func (m Model) needsLines(w int) []string {
	b := &m.board
	if !b.have {
		return []string{dimStyle.Render("  reading the server…")}
	}
	if len(b.data.Needs) == 0 {
		return []string{dimStyle.Render("  Nothing needs you")}
	}
	var out []string
	for i, n := range b.data.Needs {
		head := m.cursorFor(b.panel == boardNeeds, i == b.cursor[boardNeeds]) + errorStyle.Render("!") + " "
		out = append(out, head+ansi.Truncate(n.Text, max(w-lipgloss.Width(head), 1), "…"))
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
	if b.data.Mine != nil {
		return m.mineLines()
	}
	share := 0.0
	if a.Online > 0 {
		share = float64(a.Busy) / float64(a.Online)
	}
	out := []string{
		fmt.Sprintf("  %s %s", bar(share, 10), view.AgentSummaryText(a)),
		chartLine(b, inner),
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
	if m.hintMine() {
		out = append(out, dimStyle.Render("  Show only your agents: bam agent mine add NAME-START"))
	}
	return out
}

// chartLine draws the busy and queued charts with their ranges, as wide as
// inner allows; the legend is left out when the charts would have no room.
func chartLine(b *boardState, inner int) string {
	busy, queued := sparkRange(b.busy, pct), sparkRange(b.queued, count)
	const legend = "  since opened, lowest–highest"
	fixed := lipgloss.Width("  busy  " + busy + "  queued  " + queued)
	sw := min((inner-fixed-lipgloss.Width(legend))/2, 12)
	if sw < 4 {
		sw = max(min((inner-fixed)/2, 12), 1)
		return fmt.Sprintf("  busy %s %s  queued %s %s",
			strings.TrimRight(spark(b.busy, 1, sw), " "), busy, strings.TrimRight(spark(b.queued, 0, sw), " "), queued)
	}
	return fmt.Sprintf("  busy %s %s  queued %s %s%s",
		strings.TrimRight(spark(b.busy, 1, sw), " "), busy, strings.TrimRight(spark(b.queued, 0, sw), " "), queued, legend)
}

func pct(v float64) string   { return fmt.Sprintf("%.0f%%", v*100) }
func count(v float64) string { return fmt.Sprintf("%.0f", v) }

// sparkRange is the lowest and highest value a chart has drawn since the
// board opened, so its blocks have a scale.
func sparkRange(s []float64, f func(float64) string) string {
	if len(s) == 0 {
		return "–"
	}
	return f(slices.Min(s)) + "–" + f(slices.Max(s))
}

// hintMine says whether the agents panel carries the line that says how to
// show only your agents: when none are listed and the terminal has the room.
func (m Model) hintMine() bool {
	return m.board.have && m.board.data.Agents != nil && !m.board.data.MineSet && m.height >= 24
}

// mineStyle colors an agent's mark by its state.
func mineStyle(s app.AgentState) lipgloss.Style {
	switch s {
	case app.AgentBuilding:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case app.AgentDisabled:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	case app.AgentDown:
		return errorStyle
	}
	return dimStyle
}

// mineLines is the Your agents panel: the four counts, one row per pool with
// a mark per agent, the names of the agents that are down or disabled, and a
// row for each name start no agent matches. The cursor moves over the pools.
func (m Model) mineLines() []string {
	b := &m.board
	mine := b.data.Mine
	paint := func(s app.AgentState, mark string) string { return mineStyle(s).Render(mark) }
	out := []string{"  " + view.MineSummary(mine)}
	for i, p := range mine.Pools {
		out = append(out, m.cursorFor(b.panel == boardAgents, i == b.cursor[boardAgents])+view.MinePoolLine(p, paint))
	}
	for _, a := range mine.Away {
		out = append(out, "  "+mineStyle(a.State).Render("!")+" "+view.MineAwayLine(a))
	}
	for _, s := range mine.NoMatch {
		out = append(out, dimStyle.Render("  "+view.MineNoMatchLine(s)))
	}
	return out
}

// fitMine keeps the Your agents lines within h rows. The counts and pools
// stay, since the cursor scrolls over the pools; when the down, disabled and
// unmatched rows after them do not all fit, the last row that fits says how
// many are left out, so none is cut without notice.
func fitMine(lines []string, mine *app.MyAgents, h int) []string {
	if len(lines) <= h {
		return lines
	}
	head := 1 + len(mine.Pools)
	keep := max(h-head-1, 0)
	hidden := lines[head+keep:]
	away := max(len(mine.Away)-keep, 0)
	unmatched := len(hidden) - away
	var parts []string
	if away > 0 {
		parts = append(parts, fmt.Sprintf("%d more down or disabled", away))
	}
	if unmatched == 1 {
		parts = append(parts, "1 start matches no agent")
	} else if unmatched > 1 {
		parts = append(parts, fmt.Sprintf("%d starts match no agent", unmatched))
	}
	return append(lines[:head+keep:head+keep], dimStyle.Render("  … "+strings.Join(parts, " · ")))
}

// agentCursorLine is the line of the Your agents panel the cursor is on.
func (m Model) agentCursorLine() int {
	if m.board.data.Mine == nil {
		return 0
	}
	return 1 + m.board.cursor[boardAgents]
}

// queueTitle names the queue panel by what it lists.
func (m Model) queueTitle() string {
	d := m.board.data
	switch {
	case !m.board.have || !d.QueueOK:
		return "Queue"
	case d.QueueFiltered:
		return fmt.Sprintf("Your queue · %d of %d", len(d.Queue), len(d.Queue)+d.QueueOthers)
	}
	return fmt.Sprintf("Queue · %d", len(d.Queue))
}

// agentsTitle names the agents panel by what it shows.
func (m Model) agentsTitle() string {
	switch {
	case m.board.whole && m.board.data.MineSet:
		return "Agents · whole server"
	case m.board.data.MineSet:
		return "Your agents"
	}
	return "Agents"
}

// boardToggleWhole is a: your agents only, or every agent of the server. It
// does nothing when no agents are listed.
func (m Model) boardToggleWhole() (tea.Model, tea.Cmd) {
	if !m.board.data.MineSet && !m.board.data.QueueFiltered && !m.board.whole {
		m.status = "nothing is listed as yours: bam agent mine add NAME-START"
		return m, nil
	}
	m.board.whole = !m.board.whole
	m.board.data.Mine = nil // the old reading is the other view's
	m.board.panel = boardNeeds
	return m, m.loadBoard(true)
}

// boardPoolInfo is enter on a pool of Your agents: its agents and their health.
func (m Model) boardPoolInfo() (tea.Model, tea.Cmd) {
	mine := m.board.data.Mine
	c := m.board.cursor[boardAgents]
	if mine == nil || c >= len(mine.Pools) {
		return m, nil
	}
	p := mine.Pools[c]
	var sb strings.Builder
	for _, a := range p.Agents {
		fmt.Fprintf(&sb, "%s %s\n", view.MineMark(a.State), view.MineAwayLineOrName(a))
	}
	return m.openInfo(p.Name+"…", sb.String()), nil
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
	case len(b.data.Queue) == 0 && b.data.QueueOthers == 0:
		return []string{dimStyle.Render("  The queue is empty")}, 0
	case len(b.data.Queue) == 0:
		return []string{dimStyle.Render("  Nothing waits for you"), dimStyle.Render("  " + view.OthersText(b.data.QueueOthers))}, 0
	}
	waits := make([]string, len(b.data.Queue))
	posW, keyW, waitW := 0, 0, 0
	for i, r := range b.data.Queue {
		// Waited is as of the last read; it keeps counting until the next.
		w := r.Waited + max(m.now().Sub(b.loadedAt), 0)
		waits[i] = brief(w)
		if l, ok := app.LongWait(w); ok {
			waits[i] = l
		}
		if w <= 0 {
			waits[i] = "0s"
		}
		if r.AtLeast {
			waits[i] = "–" // bam did not read this build's wait
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
	if b.data.QueueOthers > 0 {
		out = append(out, dimStyle.Render("  "+view.OthersText(b.data.QueueOthers)))
	}
	return out, b.cursor[boardQueue]
}

// pinLines draws the groups, and says which line holds the cursor.
func (m Model) pinLines(inner int, spaced bool) ([]string, int) {
	b := &m.board
	if !b.have {
		return []string{dimStyle.Render("  reading the pipelines…")}, 0
	}
	if len(b.data.Groups) == 0 {
		return []string{dimStyle.Render("  No pipelines pinned. Pin one with: bam pin add GROUP PLAN")}, 0
	}
	var out []string
	cursorLine, n := 0, 0
	labelW := m.pinLabelWidth(inner)
	for gi, g := range b.data.Groups {
		if spaced && gi > 0 {
			out = append(out, "")
		}
		out = append(out, "  "+titleStyle.Render(g.Name))
		for _, p := range g.Pins {
			sel := b.panel == boardPins && n == b.cursor[boardPins]
			if sel {
				cursorLine = len(out)
			}
			out = append(out, m.cursorFor(b.panel == boardPins, n == b.cursor[boardPins])+m.pinText(p, labelW))
			n++
		}
	}
	return out, cursorLine
}

// pinRest is the width of a pin's row besides its name: the results, the
// current build, and the last and usual times, with the spaces between.
const pinRest = 61

// pinLabelWidth is the width of the name column of Pinned: the longest name
// of a pin, at least 14, and no wider than what the panel's inner width leaves
// after the rest of the row.
func (m Model) pinLabelWidth(inner int) int {
	w := 14
	for _, g := range m.board.data.Groups {
		for _, p := range g.Pins {
			if p.Err == nil {
				w = max(w, lipgloss.Width(p.Label()))
			}
		}
	}
	return min(w, max(14, inner-2-pinRest))
}

func (m Model) pinText(p app.PinView, labelW int) string {
	if p.Err != nil {
		return fmt.Sprintf("%-22s %s", p.Label(), errorStyle.Render("could not be read: "+p.Err.Error()))
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
	label := truncate(p.Label(), labelW)
	label += strings.Repeat(" ", max(labelW-lipgloss.Width(label), 0))
	return fmt.Sprintf("%s %s %-24s last %s · usual %s", label, results(sb.String(), len(p.Results)), now, brief(p.Last), brief(p.Usual))
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
