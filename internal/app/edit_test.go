package app

import (
	"strings"
	"testing"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/stretchr/testify/assert"
)

func editRef() PlanRef {
	tgt := config.ResolvedTarget{Name: "provision-lab", Target: config.Target{Plan: "PROJ-PROV", Branch: "develop",
		Defaults: config.StringMap{"cluster_type": "k8s", "db_password": "${LAB_DB_PASSWORD}"},
		Options:  config.StringListMap{"cluster_type": {"k8s", "dcos"}, "region": {"eu", "us"}},
		Required: []string{"cluster_name", "ticket"},
	}}
	return PlanRef{PlanKey: "PROJ-PROV12", MasterKey: "PROJ-PROV", Branch: "develop", Target: &tgt}
}

// editOpened is what ApplyVars would give for editRef with --from 8 and
// --var cluster_name=alpha.
func editOpened() VarSet {
	return VarSet{DeclaredKnown: true,
		Declared: map[string]bool{"api_token": true, "cluster_name": true, "cluster_type": true, "compute_nodes": true},
		Vars: []ResolvedVar{
			{Name: "api_token", Value: "********", PlanValue: "********", Source: "plan", Declared: true, Secret: true},
			{Name: "cluster_name", Value: "alpha", Source: "flag", Declared: true},
			{Name: "cluster_type", Value: "k8s", PlanValue: "k8s", Source: "preset", Declared: true},
			{Name: "compute_nodes", Value: "3", PlanValue: "1", Source: "from #8", Declared: true},
			{Name: "db_password", Value: "${LAB_DB_PASSWORD}", Source: "preset", Secret: true},
		}}
}

const editHeader = "" +
	"# bam run provision-lab · PROJ-PROV · branch develop · server work\n" +
	"# One variable per line, name=value. Save and quit to run.\n" +
	"# ******** keeps a secret's current value. A value of exactly ${NAME} reads the environment.\n" +
	"# Delete a line to keep its value. Delete every line, or quit with an error (:cq), to abort.\n" +
	"\n"

const editRowsText = "" +
	"# plan, secret\n" +
	"api_token=********\n" +
	"# flag (plan: empty), required\n" +
	"cluster_name=alpha\n" +
	"# preset, options: k8s|dcos\n" +
	"cluster_type=k8s\n" +
	"# from #8 (plan: \"1\")\n" +
	"compute_nodes=3\n" +
	"# preset, ${LAB_DB_PASSWORD} unset, secret\n" +
	"db_password=********\n" +
	"# options: eu|us\n" +
	"region=\n" +
	"# required\n" +
	"ticket=\n"

func TestEditBufferRendersEveryRow(t *testing.T) {
	got := EditBuffer(editRef(), editOpened(), emptyEnv, "work", false, nil)
	assert.Equal(t, editHeader+editRowsText, string(got))
}

func TestEditBufferNeverHoldsASecret(t *testing.T) {
	env := func(k string) string {
		if k == "LAB_DB_PASSWORD" {
			return "hunter2"
		}
		return ""
	}
	opened := editOpened()
	opened.Vars[0].Value = "tok-123" // a secret the user passed with --var
	got := string(EditBuffer(editRef(), opened, env, "work", false, nil))
	assert.NotContains(t, got, "hunter2")
	assert.NotContains(t, got, "tok-123")
	assert.Contains(t, got, "# preset, ${LAB_DB_PASSWORD} set, secret\ndb_password=********\n")
}

func TestEditBufferShowsAnEmptySecretAsEmpty(t *testing.T) {
	opened := editOpened()
	opened.Vars[0].Value = ""
	got := string(EditBuffer(editRef(), opened, emptyEnv, "work", false, nil))
	assert.Contains(t, got, "\napi_token=\n")
}

func TestEditBufferForAPlanOnTheDefaultBranch(t *testing.T) {
	ref := PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD"}
	got := string(EditBuffer(ref, VarSet{}, emptyEnv, "work", true, nil))
	assert.True(t, strings.HasPrefix(got,
		"# bam run PROJ-BUILD · default branch · server work\n"+
			"# One variable per line, name=value. Save and quit to preview.\n"), got)
}

func TestEditBufferPutsProblemsFirst(t *testing.T) {
	got := string(EditBuffer(editRef(), editOpened(), emptyEnv, "work", false,
		[]string{"cluster_type=\"nomad\" is not allowed by preset provision-lab\nallowed: k8s, dcos", "line 3 is not name=value"}))
	assert.Equal(t, ""+
		"# error: cluster_type=\"nomad\" is not allowed by preset provision-lab\n"+
		"#   allowed: k8s, dcos\n"+
		"# error: line 3 is not name=value\n"+
		"#\n"+
		editHeader+editRowsText, got)
}

func TestReopenBufferReplacesTheBlock(t *testing.T) {
	user := "# my note\ncluster_name=beta\n"
	once := ReopenBuffer([]byte(user), []string{"first\nwhy"})
	assert.Equal(t, "# error: first\n#   why\n#\n"+user, string(once))

	twice := ReopenBuffer(once, []string{"second"})
	assert.Equal(t, "# error: second\n#\n"+user, string(twice), "blocks never stack")

	assert.Equal(t, user, string(ReopenBuffer(twice, nil)), "no problems, no block")
	assert.Equal(t, user, string(ReopenBuffer([]byte(user), nil)), "text without a block is untouched")
}

// setLine replaces the line of name, or appends one when there is none.
func setLine(buf, name, value string) string {
	lines := strings.Split(buf, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, name+"=") {
			lines[i] = name + "=" + value
			return strings.Join(lines, "\n")
		}
	}
	return buf + name + "=" + value + "\n"
}

func dropLine(buf, name string) string {
	var out []string
	for _, l := range strings.Split(buf, "\n") {
		if !strings.HasPrefix(l, name+"=") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestParseEditsFindsOnlyChanges(t *testing.T) {
	start := string(EditBuffer(editRef(), editOpened(), emptyEnv, "work", false, []string{"old problem"}))
	for _, tc := range []struct {
		name string
		text string
		want []Edit
	}{
		{"unchanged", start, nil},
		{"changed value", setLine(start, "cluster_name", "beta"), []Edit{{"cluster_name", "beta"}}},
		{"emptied value", setLine(start, "cluster_name", ""), []Edit{{"cluster_name", ""}}},
		{"secret replaced", setLine(start, "api_token", "tok-9"), []Edit{{"api_token", "tok-9"}}},
		{"env secret replaced by a reference", setLine(start, "db_password", "${OTHER}"), []Edit{{"db_password", "${OTHER}"}}},
		{"deleted line keeps its value", dropLine(start, "compute_nodes"), nil},
		{"new name", start + "extra=1\n", []Edit{{"extra", "1"}}},
		{"required row filled", setLine(start, "ticket", "T-1"), []Edit{{"ticket", "T-1"}}},
		{"hash inside a value", setLine(start, "cluster_name", "a#b"), []Edit{{"cluster_name", "a#b"}}},
		{"carriage return stripped", setLine(start, "cluster_name", "beta\r"), []Edit{{"cluster_name", "beta"}}},
		{"spaces around the name", strings.Replace(start, "cluster_name=alpha", "  cluster_name =beta", 1), []Edit{{"cluster_name", "beta"}}},
		{"value kept verbatim", setLine(start, "cluster_name", " beta "), []Edit{{"cluster_name", " beta "}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edits, abort, problems := ParseEdits([]byte(tc.text), editRef(), editOpened())
			assert.False(t, abort)
			assert.Empty(t, problems)
			assert.Equal(t, tc.want, edits)
		})
	}
}

func TestParseEditsReportsProblemsWithoutValues(t *testing.T) {
	text := "" +
		"# header\n" + // 1
		"hunter2\n" + // 2: no '='
		"db pass=hunter2\n" + // 3: bad name
		"cluster_name=a\n" + // 4
		"cluster_name=hunter2\n" + // 5: duplicate
		"cluster_type=********\n" + // 6: the mask on a non-secret
		"api_token=********\n" // 7: fine, keeps the secret
	edits, abort, problems := ParseEdits([]byte(text), editRef(), editOpened())
	assert.False(t, abort)
	assert.Equal(t, []string{
		"line 2 is not name=value",
		`line 3: name "db pass" may use only letters, digits, _ . -`,
		"line 5: cluster_name is also on line 4",
		"line 6: cluster_type: ******** is the mask; type the real value",
	}, problems)
	assert.Equal(t, []Edit{{"cluster_name", "a"}}, edits, "good lines still parse")
	for _, p := range problems {
		assert.NotContains(t, p, "hunter2")
	}
}

// TestEditBufferKeepsMultiLineAndInvalidNamesUnedited covers final review
// finding 1: a non-secret value containing "\n" or "\r", or a row whose name
// cannot be typed back (fails editNameRe), must never appear as a
// name=value line. Only its comment goes in the buffer, so a save-unchanged
// round trip cannot truncate the value or inject a bogus variable.
func TestEditBufferKeepsMultiLineAndInvalidNamesUnedited(t *testing.T) {
	ref := editRef()
	opened := editOpened()
	opened.Declared["multiline"] = true
	opened.Declared["cr_value"] = true
	opened.Vars = append(opened.Vars,
		ResolvedVar{Name: "multiline", Value: "a\nFOO=bar", PlanValue: "a\nFOO=bar", Source: "plan", Declared: true},
		ResolvedVar{Name: "cr_value", Value: "a\rb", PlanValue: "a\rb", Source: "plan", Declared: true},
		ResolvedVar{Name: "bad name", Value: "x", PlanValue: "x", Source: "plan", Declared: false},
	)

	got := string(EditBuffer(ref, opened, emptyEnv, "work", false, nil))
	assert.NotContains(t, got, "a\nFOO=bar")
	assert.NotContains(t, got, "FOO=bar")
	assert.NotContains(t, got, "a\rb")
	assert.NotContains(t, got, "bad name=x")
	assert.Contains(t, got, "# plan, multi-line value, kept as is\n")
	assert.Contains(t, got, "# plan, name cannot be edited here, kept as is\n")

	edits, abort, problems := ParseEdits([]byte(got), ref, opened)
	assert.False(t, abort)
	assert.Empty(t, problems)
	assert.Empty(t, edits, "a save-unchanged round trip must produce no edits")
}

func TestParseEditsAbortsOnAnEmptyBuffer(t *testing.T) {
	for _, text := range []string{"", "\n\n", "# only\n  # comments\n"} {
		_, abort, problems := ParseEdits([]byte(text), editRef(), editOpened())
		assert.True(t, abort, "%q", text)
		assert.Empty(t, problems)
	}
}

func TestEditBufferHeaderNamesTheRevision(t *testing.T) {
	ref := PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD", Revision: "abc1234"}
	got := string(EditBuffer(ref, VarSet{}, func(string) string { return "" }, "lab", false, nil))
	assert.Contains(t, got, "# bam run PROJ-BUILD · default branch · revision abc1234 · server lab\n")

	ref.Revision = ""
	got = string(EditBuffer(ref, VarSet{}, func(string) string { return "" }, "lab", false, nil))
	assert.NotContains(t, got, "revision")
	ref.Revision, ref.RevisionFrom = "9b30b9aba03a4f45881629b03531006b89c1e0e7", "PROJ-BUILD-41"
	got = string(EditBuffer(ref, VarSet{}, func(string) string { return "" }, "lab", false, nil))
	assert.Contains(t, got, "# bam run PROJ-BUILD · default branch · revision 9b30b9a (from #41) · server lab\n")
}
