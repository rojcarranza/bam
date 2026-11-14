package tui

// rowKind says what a board row is. With a key it names the row.
type rowKind string

const (
	rowNeed  rowKind = "need"  // a Needs you line
	rowQueue rowKind = "queue" // a queued build
	rowPool  rowKind = "pool"  // an agent pool of Your agents
	rowPin   rowKind = "pin"   // a pin's own row, or one of its branch rows
	rowChild rowKind = "child" // a pipeline of a pin's tree
)

// boardRow is the identity of one board row: its kind and a key that stays
// the same when a read adds, removes or reorders the rows around it. The
// selection of each panel is a boardRow, found again after every read, so a
// key like P acts on the row the person chose, never on the row that now
// sits at the same number. A click sets the cursor, and the next read holds
// the row it landed on the same way.
type boardRow struct {
	Kind rowKind
	Key  string
	// Parent is the row to fall back to when this one is gone: the pin of a
	// pipeline of its tree.
	Parent string
}

func pinRowKey(p pinSel) string {
	return p.pin.Pin + "|" + p.pin.PlanKey + "|" + p.pin.Branch
}

// boardRows is the rows of a panel in screen order, each as its identity.
func (b *boardState) boardRows(p boardPanel) []boardRow {
	var out []boardRow
	switch p {
	case boardNeeds:
		for _, n := range b.data.Needs {
			out = append(out, boardRow{Kind: rowNeed, Key: needID(n)})
		}
	case boardQueue:
		for _, q := range b.data.Queue {
			out = append(out, boardRow{Kind: rowQueue, Key: q.Key})
		}
	case boardAgents:
		if b.data.Mine != nil {
			for _, pl := range b.data.Mine.Pools {
				out = append(out, boardRow{Kind: rowPool, Key: pl.Name})
			}
		}
	default:
		for _, s := range b.pinSels() {
			if s.node != nil {
				out = append(out, boardRow{Kind: rowChild, Key: pinRowKey(s) + "|" + s.node.Build.Key, Parent: pinRowKey(s)})
			} else {
				out = append(out, boardRow{Kind: rowPin, Key: pinRowKey(s)})
			}
		}
	}
	return out
}

// heldRows is the row each panel's cursor is on, before a read changes the
// rows. A panel with no row under its cursor holds the zero boardRow.
func (b *boardState) heldRows() [boardPanels]boardRow {
	var held [boardPanels]boardRow
	if !b.have {
		return held // nothing was shown yet, so nothing is held
	}
	for p := range held {
		rows := b.boardRows(boardPanel(p))
		if c := b.cursor[p]; c >= 0 && c < len(rows) {
			held[p] = rows[c]
		}
	}
	return held
}

// followRows puts every panel's cursor back on its held row in the rows just
// read: on the row itself, else on the pin a pipeline of its tree belonged
// to, else where it was, kept inside the panel.
func (b *boardState) followRows(held [boardPanels]boardRow) {
	for p, h := range held {
		if h == (boardRow{}) {
			continue
		}
		fallback := -1
		for i, r := range b.boardRows(boardPanel(p)) {
			if r.Kind == h.Kind && r.Key == h.Key {
				b.cursor[p] = i
				fallback = -2
				break
			}
			if h.Parent != "" && r.Kind == rowPin && r.Key == h.Parent {
				fallback = i
			}
		}
		if fallback >= 0 {
			b.cursor[p] = fallback
		}
	}
	b.clamp()
}
