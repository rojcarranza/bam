package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func keepBuffer(in string) (string, error) { return in, nil }

// setVar answers the editor by setting name=value, appending the line when
// the buffer has none.
func setVar(pairs ...string) func(string) (string, error) {
	return func(in string) (string, error) {
		lines := strings.Split(in, "\n")
		for i := 0; i < len(pairs); i += 2 {
			name, value, found := pairs[i], pairs[i+1], false
			for j, l := range lines {
				if strings.HasPrefix(l, name+"=") {
					lines[j], found = name+"="+value, true
				}
			}
			if !found {
				lines = append(lines, name+"="+value)
			}
		}
		return strings.Join(lines, "\n"), nil
	}
}

func editHarness(t *testing.T, replies ...func(string) (string, error)) *harness {
	h := newHarness(t)
	h.tty = true
	h.vars["NO_COLOR"] = "1"
	h.vars["TERM"] = "dumb" // NO_COLOR strips color; TERM=dumb strips the hyperlink escapes too
	h.editorReplies = replies
	h.fake.TriggerResult = queuedBuild("PROJ-PROV12-9")
	return h
}

func TestRunEditNeedsATerminal(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("run", "provision-lab", "--edit"))
	assert.Contains(t, h.stderr.String(), "--edit needs a terminal")
	assert.Contains(t, h.stderr.String(), "pass the values with --var")
	assert.Empty(t, h.editorSeen)
	assert.Empty(t, h.connectOpts, "no network before the terminal check")
}

func TestRunEditTriggersTheEditedSet(t *testing.T) {
	h := editHarness(t, setVar("cluster_name", "alpha"))
	assert.Equal(t, 0, h.run("run", "provision-lab", "--edit"))

	require.Len(t, h.editorSeen, 1)
	first := h.editorSeen[0]
	assert.True(t, strings.HasPrefix(first, "# error: cluster_name is required by target provision-lab\n#\n"),
		"a fixable rule error opens the editor instead of failing:\n%s", first)
	assert.Contains(t, first, "# bam run provision-lab · PROJ-PROV · branch develop · server work\n")
	assert.Contains(t, first, "# target, options: k8s|dcos\ncluster_type=k8s\n")
	assert.Equal(t, []string{"vi"}, h.editorCmds)

	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, "PROJ-PROV12", h.fake.Triggered[0].PlanKey)
	assert.Equal(t, map[string]string{"cluster_name": "alpha"}, h.fake.Triggered[0].Variables)
	assert.Contains(t, h.stdout.String(), "cluster_name=alpha (edit)")
	assert.Contains(t, h.stdout.String(), "Queued   PROJ-PROV12-9")
}

func TestRunEditUnchangedMatchesAPlainRun(t *testing.T) {
	h := editHarness(t, keepBuffer)
	assert.Equal(t, 0, h.run("run", "provision-lab", "--var", "cluster_name=a", "--from", "last", "--edit"))
	h.tty = false
	assert.Equal(t, 0, h.run("run", "provision-lab", "--var", "cluster_name=a", "--from", "last"))
	require.Len(t, h.fake.Triggered, 2)
	assert.Equal(t, h.fake.Triggered[1], h.fake.Triggered[0])
}

func TestRunEditUsesTheConfiguredEditor(t *testing.T) {
	h := editHarness(t, setVar("cluster_name", "alpha"))
	h.vars["BAM_EDITOR"] = "code --wait"
	assert.Equal(t, 0, h.run("run", "provision-lab", "--edit"))
	assert.Equal(t, []string{"code --wait"}, h.editorCmds)
}

func TestRunEditDryRun(t *testing.T) {
	h := editHarness(t, setVar("cluster_name", "alpha"))
	assert.Equal(t, 0, h.run("run", "provision-lab", "--edit", "--dry-run"))
	assert.Contains(t, h.editorSeen[0], "Save and quit to preview.")
	assert.Contains(t, h.stdout.String(), "cluster_name=alpha (edit)")
	assert.Contains(t, h.stderr.String(), "dry run: nothing triggered")
	assert.Empty(t, h.fake.Triggered)
}

func TestRunEditJSON(t *testing.T) {
	h := editHarness(t, setVar("cluster_name", "alpha"))
	assert.Equal(t, 0, h.run("run", "provision-lab", "--var", "db_password=hunter2", "--edit", "--json"))
	var doc struct {
		Key       string            `json:"key"`
		Variables map[string]string `json:"variables"`
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "PROJ-PROV12-9", doc.Key)
	assert.Equal(t, map[string]string{"cluster_name": "alpha", "db_password": "********"}, doc.Variables)
	assert.NotContains(t, h.stdout.String(), "hunter2")
}

func TestRunEditWatch(t *testing.T) {
	h := editHarness(t, setVar("cluster_name", "alpha"))
	h.tty = true
	h.fake.Sequences = map[string][]provider.Build{"PROJ-PROV12-9": {
		{Key: "PROJ-PROV12-9", State: provider.StateRunning},
		{Key: "PROJ-PROV12-9", State: provider.StateSuccess},
	}}
	assert.Equal(t, 0, h.run("run", "provision-lab", "--edit", "--watch", "--json"))
	lines := strings.Split(strings.TrimSpace(h.stdout.String()), "\n")
	var last map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &last))
	assert.Equal(t, "done", last["type"])
	require.Len(t, h.fake.Triggered, 1)
}

func TestRunEditAbort(t *testing.T) {
	for name, reply := range map[string]func(string) (string, error){
		"empty buffer":     func(string) (string, error) { return "# nothing left\n", nil },
		"editor cancelled": func(in string) (string, error) { return in, errEditorCancelled },
	} {
		t.Run(name, func(t *testing.T) {
			h := editHarness(t, reply)
			assert.Equal(t, 130, h.run("run", "provision-lab", "--var", "cluster_name=a", "--edit"))
			assert.Equal(t, "run aborted: nothing triggered\n", h.stderr.String())
			assert.Empty(t, h.fake.Triggered)
		})
	}
}

func TestRunEditInterruptedWhileEditing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := editHarness(t, func(in string) (string, error) { cancel(); return in, nil })
	assert.Equal(t, 130, h.runCtx(ctx, "run", "provision-lab", "--var", "cluster_name=a", "--edit"))
	assert.Empty(t, h.fake.Triggered)
}

func TestRunEditReopensOnErrors(t *testing.T) {
	var second, third string
	h := editHarness(t,
		func(in string) (string, error) {
			out, _ := setVar("cluster_name", "alpha", "cluster_type", "nomad")(in)
			return out + "oops\n", nil
		},
		func(in string) (string, error) {
			second = in
			out, _ := setVar("cluster_type", "dcos")(in)
			return strings.Replace(out, "oops\n", "", 1), nil
		},
		func(in string) (string, error) { third = in; return in, nil },
	)
	// The third reply is never used: the second buffer is valid.
	assert.Equal(t, 0, h.run("run", "provision-lab", "--edit"))
	assert.Empty(t, third)
	require.Len(t, h.editorSeen, 2)

	assert.True(t, strings.HasPrefix(second, "# error: line "), second)
	assert.Contains(t, second, " is not name=value\n")
	assert.Contains(t, second, "# error: cluster_type=\"nomad\" is not allowed by target provision-lab\n#   allowed: k8s, dcos\n")
	assert.Equal(t, 2, strings.Count(second, "# error: "), "the first round's block is replaced, not stacked")
	assert.Contains(t, second, "cluster_type=nomad\n", "the user's own text comes back")

	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, map[string]string{"cluster_name": "alpha", "cluster_type": "dcos"}, h.fake.Triggered[0].Variables)
}

func TestRunEditNeverShowsASecret(t *testing.T) {
	h := editHarness(t,
		func(in string) (string, error) { return in + "oops\n", nil }, // force a reopen
		func(in string) (string, error) { return strings.Replace(in, "oops\n", "", 1), nil },
	)
	yaml := strings.Replace(projectYAML, "      cluster_type: k8s\n",
		"      cluster_type: k8s\n      db_password: ${LAB_DB_PASSWORD}\n", 1)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(yaml), 0o644))
	h.vars["LAB_DB_PASSWORD"] = "hunter2"

	assert.Equal(t, 0, h.run("run", "provision-lab", "--var", "cluster_name=a", "--edit", "--debug"))
	require.Len(t, h.editorSeen, 2)
	for _, buf := range h.editorSeen {
		assert.NotContains(t, buf, "hunter2")
		assert.Contains(t, buf, "db_password=********")
	}
	assert.NotContains(t, h.stdout.String(), "hunter2")
	assert.NotContains(t, h.stderr.String(), "hunter2")
	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, "hunter2", h.fake.Triggered[0].Variables["db_password"], "the untouched secret is still sent")
}

// TestRunEditNeverShowsASecretPassedAsVar covers final review finding 4: a
// secret passed with --var, not a target default, must still be masked in
// every buffer the editor sees, while the real value still reaches the
// trigger.
func TestRunEditNeverShowsASecretPassedAsVar(t *testing.T) {
	h := editHarness(t, keepBuffer)
	assert.Equal(t, 0, h.run("run", "provision-lab", "--var", "cluster_name=a", "--var", "db_password=hunter2", "--edit"))

	require.Len(t, h.editorSeen, 1)
	for _, buf := range h.editorSeen {
		assert.NotContains(t, buf, "hunter2")
		assert.Contains(t, buf, "db_password=********")
	}
	assert.NotContains(t, h.stdout.String(), "hunter2")
	assert.NotContains(t, h.stderr.String(), "hunter2")
	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, "hunter2", h.fake.Triggered[0].Variables["db_password"], "the value passed with --var is still sent")
}

func TestRunEditHeaderNamesTheRevision(t *testing.T) {
	h := editHarness(t, keepBuffer)
	assert.Equal(t, 0, h.run("run", "provision-lab", "--edit", "--revision", "abc1234", "--var", "cluster_name=a", "--dry-run"))
	require.Len(t, h.editorSeen, 1)
	assert.Contains(t, h.editorSeen[0], "· revision abc1234 · server ")
}

func TestRunEditHeaderNamesAReusedRevision(t *testing.T) {
	h := editHarness(t, keepBuffer)
	h.fake.History["PROJ-PROV12"][0].Revision = "9b30b9aba03a4f45881629b03531006b89c1e0e7"
	key := h.fake.History["PROJ-PROV12"][0].Key
	assert.Equal(t, 0, h.run("run", "provision-lab", "--edit", "--from", key, "--revision", "from", "--var", "cluster_name=a", "--dry-run"))
	require.Len(t, h.editorSeen, 1)
	assert.Contains(t, h.editorSeen[0], "· revision 9b30b9a (from "+app.BuildLabel(key)+") · server ")
}
