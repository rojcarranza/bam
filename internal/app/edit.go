package app

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/r0jjames/bam-cli/internal/config"
)

// The buffer of bam run --edit (run-edit spec §4). Render and parse live
// together because they are one round-trip contract: the ******** sentinel,
// the edit diff and the error block must agree. The functions return bytes
// and never touch a stream, so the terminal UI can reuse them.

const (
	errorPrefix = "# error: "
	errorMore   = "#   "
)

// editRow is one variable line as it is first shown.
type editRow struct {
	Name     string
	Shown    string // the value on the line: ******** for a secret with a value
	Secret   bool
	Required bool
	Options  []string
	V        ResolvedVar // zero when the row exists only for required/options
}

// editRows is the opened set plus the target's required and options names it
// lacks, sorted by name. It is the same rule as the run form's fields: a
// required name the plan does not declare still needs a line to be filled in.
func editRows(ref PlanRef, opened VarSet) []editRow {
	required := map[string]bool{}
	options := map[string][]string{}
	if t := ref.Target; t != nil {
		for _, n := range t.Required {
			required[n] = true
		}
		for n, vals := range t.Options {
			options[n] = vals
		}
	}
	rows := map[string]editRow{}
	for _, v := range opened.Vars {
		shown := v.Value
		if v.Secret && v.Value != "" {
			shown = MaskedDisplay
		}
		rows[v.Name] = editRow{Name: v.Name, Shown: shown, Secret: v.Secret, V: v}
	}
	for n := range required {
		if _, ok := rows[n]; !ok {
			rows[n] = editRow{Name: n, Secret: IsSecretName(n)}
		}
	}
	for n := range options {
		if _, ok := rows[n]; !ok {
			rows[n] = editRow{Name: n, Secret: IsSecretName(n)}
		}
	}
	out := make([]editRow, 0, len(rows))
	for _, n := range sortedNames(rows) {
		r := rows[n]
		r.Required, r.Options = required[n], options[n]
		out = append(out, r)
	}
	return out
}

// comment is the line above a row: source, env reference, required,
// options, secret, and an optional trailing note. It never holds a secret
// value.
func (r editRow) comment(getenv func(string) string, note string) string {
	var parts []string
	if src := r.V.Source; src != "" {
		if !r.Secret && r.V.Declared && src != "plan" && r.V.Value != r.V.PlanValue {
			plan := "empty"
			if r.V.PlanValue != "" {
				plan = fmt.Sprintf("%q", r.V.PlanValue)
			}
			src += " (plan: " + plan + ")"
		}
		parts = append(parts, src)
	}
	if r.V.Source == "target" {
		if name, ok := config.EnvRef(r.V.Value); ok {
			state := "unset"
			if getenv(name) != "" {
				state = "set"
			}
			parts = append(parts, "${"+name+"} "+state)
		}
	}
	if r.Required {
		parts = append(parts, "required")
	}
	if len(r.Options) > 0 {
		parts = append(parts, "options: "+strings.Join(r.Options, "|"))
	}
	if r.Secret {
		parts = append(parts, "secret")
	}
	if note != "" {
		parts = append(parts, note)
	}
	if len(parts) == 0 {
		return ""
	}
	return "# " + strings.Join(parts, ", ") + "\n"
}

// EditBuffer renders the buffer bam run --edit opens: an error block for
// problems, a header, then one commented name=value line per variable.
func EditBuffer(ref PlanRef, opened VarSet, getenv func(string) string, server string, dryRun bool, problems []string) []byte {
	var b strings.Builder
	head := []string{"bam run " + ref.MasterKey}
	if ref.Target != nil {
		head = []string{"bam run " + ref.Target.Name, ref.MasterKey}
	}
	if ref.Branch != "" {
		head = append(head, "branch "+ref.Branch)
	} else {
		head = append(head, "default branch")
	}
	if ref.Revision != "" {
		head = append(head, "revision "+ref.RevisionLabel())
	}
	head = append(head, "server "+server)
	action := "run"
	if dryRun {
		action = "preview"
	}
	fmt.Fprintf(&b, "# %s\n", strings.Join(head, " · "))
	fmt.Fprintf(&b, "# One variable per line, name=value. Save and quit to %s.\n", action)
	b.WriteString("# " + MaskedDisplay + " keeps a secret's current value. A value of exactly ${NAME} reads the environment.\n")
	b.WriteString("# Delete a line to keep its value. Delete every line, or quit with an error (:cq), to abort.\n\n")
	for _, r := range editRows(ref, opened) {
		switch {
		// A raw value that spans lines would render as more than one
		// name=value line and, saved back unchanged, would truncate the
		// value and inject a bogus variable from what follows the first
		// "\n" or "\r" (final review finding 1). Show only the comment;
		// no line at all means ParseEdits keeps the resolved value.
		case !r.Secret && strings.ContainsAny(r.Shown, "\n\r"):
			b.WriteString(r.comment(getenv, "multi-line value, kept as is"))
		// A name that ParseEdits could never accept back could never be
		// saved unchanged either, so it gets the same treatment.
		case !editNameRe.MatchString(r.Name):
			b.WriteString(r.comment(getenv, "name cannot be edited here, kept as is"))
		default:
			b.WriteString(r.comment(getenv, ""))
			b.WriteString(r.Name + "=" + r.Shown + "\n")
		}
	}
	return ReopenBuffer([]byte(b.String()), problems)
}

// ReopenBuffer replaces the error block at the top of text with one for
// problems, leaving everything else as the user left it.
func ReopenBuffer(text []byte, problems []string) []byte {
	lines := strings.SplitAfter(string(text), "\n")
	i := 0
	for i < len(lines) && strings.HasPrefix(lines[i], errorPrefix) {
		i++
		for i < len(lines) && strings.HasPrefix(lines[i], errorMore) {
			i++
		}
	}
	if i > 0 && i < len(lines) && strings.TrimRight(lines[i], "\r\n") == "#" {
		i++
	}
	var b strings.Builder
	for _, p := range problems {
		first, rest, more := strings.Cut(p, "\n")
		b.WriteString(errorPrefix + first + "\n")
		if more {
			for _, l := range strings.Split(rest, "\n") {
				b.WriteString(errorMore + l + "\n")
			}
		}
	}
	if len(problems) > 0 {
		b.WriteString("#\n")
	}
	b.WriteString(strings.Join(lines[i:], ""))
	return []byte(b.String())
}

// rowsByName indexes editRows for ParseEdits.
func rowsByName(rows []editRow) map[string]editRow {
	m := make(map[string]editRow, len(rows))
	for _, r := range rows {
		m[r.Name] = r
	}
	return m
}

// editNameRe is what a variable line's name may be.
var editNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ParseEdits reads the buffer the editor saved. It compares each line with
// the rows the buffer first opened with, never with an earlier round, and
// returns only real changes: an untouched ********, an unchanged value and
// a deleted line all leave the resolved value standing. abort is a buffer
// with no variable lines. A problem names a line and at most a name, never a
// value: the line may hold a secret the user typed.
func ParseEdits(text []byte, ref PlanRef, opened VarSet) (edits []Edit, abort bool, problems []string) {
	rows := rowsByName(editRows(ref, opened))
	seen := map[string]int{}
	count := 0
	for i, raw := range strings.Split(string(text), "\n") {
		n := i + 1
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		count++
		name, val, ok := strings.Cut(line, "=")
		if !ok {
			problems = append(problems, fmt.Sprintf("line %d is not name=value", n))
			continue
		}
		name = strings.TrimSpace(name)
		if !editNameRe.MatchString(name) {
			problems = append(problems, fmt.Sprintf("line %d: name %q may use only letters, digits, _ . -", n, name))
			continue
		}
		if first, dup := seen[name]; dup {
			problems = append(problems, fmt.Sprintf("line %d: %s is also on line %d", n, name, first))
			continue
		}
		seen[name] = n
		row, known := rows[name]
		switch {
		case known && row.Secret && val == MaskedDisplay:
		case val == MaskedDisplay:
			problems = append(problems, fmt.Sprintf("line %d: %s: %s is the mask; type the real value", n, name, MaskedDisplay))
		case known && val == row.Shown:
		default:
			edits = append(edits, Edit{Name: name, Value: val})
		}
	}
	if count == 0 {
		return nil, true, nil
	}
	return edits, false, problems
}
