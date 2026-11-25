package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rojcarranza/bam/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testForm() app.PresetForm {
	return app.PresetForm{Name: "deploy-qa", Plan: "PROJ-DEPLOY", Branch: "main", Path: "~/bam/presets/proj.yaml", Rows: []app.FormRow{
		{Name: "region", Value: "eu", Plan: "eu", Choices: []string{"eu"}},
		{Name: "target_env", Value: "staging", Plan: "qa", Choices: []string{"staging", "qa"}},
		{Name: "version", Plan: "", Choices: []string{""}},
		{Name: "deploy_token", Value: "${DEPLOY_TOKEN}", Secret: true, CanStore: true, Commented: true},
	}}
}

func formKey(m presetFormModel, ks ...tea.KeyMsg) presetFormModel {
	for _, k := range ks {
		n, _ := m.Update(k)
		m = n.(presetFormModel)
	}
	return m
}

func rk(s string) tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func kt(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// Recent values arrive after the form is open and join the choices.
func TestPresetFormRecentValuesArrive(t *testing.T) {
	m := newPresetForm(context.Background(), testForm(), func(context.Context) []app.RecentVar { return nil })
	assert.Contains(t, m.View(), "reading recent builds")
	n, _ := m.Update(recentMsg{{Name: "target_env", Values: []string{"staging", "dev"}}})
	m = n.(presetFormModel)
	assert.NotContains(t, m.View(), "reading recent builds")
	assert.Equal(t, []string{"staging", "qa", "dev"}, m.form.Rows[1].Choices)
}

// Arrows step through the choices and wrap; enter types a value and moves on.
func TestPresetFormKeys(t *testing.T) {
	m := newPresetForm(context.Background(), testForm(), nil)
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyRight))
	assert.Equal(t, "qa", m.form.Rows[1].Value)
	m = formKey(m, kt(tea.KeyRight))
	assert.Equal(t, "staging", m.form.Rows[1].Value, "wraps")
	m = formKey(m, kt(tea.KeyLeft))
	assert.Equal(t, "qa", m.form.Rows[1].Value)

	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyEnter), rk("1"), rk("."), rk("2"), kt(tea.KeyEnter))
	assert.Equal(t, "1.2", m.form.Rows[2].Value)
	assert.Equal(t, 3, m.cursor, "enter accepts and moves to the next row")

	m = formKey(m, kt(tea.KeyUp), kt(tea.KeyEnter), rk("x"), kt(tea.KeyEsc))
	assert.Equal(t, "1.2", m.form.Rows[2].Value, "esc leaves the value as it was")
	assert.False(t, m.done, "esc while typing does not leave the form")
}

// A secret row never shows or takes a value: arrows switch the stored value
// and ${ENV}, and enter edits only the variable name.
func TestPresetFormSecretRow(t *testing.T) {
	m := newPresetForm(context.Background(), testForm(), nil)
	m = formKey(m, kt(tea.KeyUp)) // clamped at the top
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown))
	assert.Contains(t, m.View(), "plan's stored value")
	m = formKey(m, kt(tea.KeyRight))
	assert.False(t, m.form.Rows[3].Commented)
	assert.Contains(t, m.View(), "${DEPLOY_TOKEN}")

	m = formKey(m, kt(tea.KeyEnter), kt(tea.KeyCtrlU))
	for _, r := range "qa_token" {
		m = formKey(m, rk(string(r)))
	}
	m = formKey(m, kt(tea.KeyEnter))
	assert.Equal(t, "${qa_token}", m.form.Rows[3].Value)

	m = formKey(m, kt(tea.KeyEnter), kt(tea.KeyCtrlU), rk("a"), rk("-"), rk("b"), kt(tea.KeyEnter))
	assert.Equal(t, "${qa_token}", m.form.Rows[3].Value, "a bad name is refused")
	assert.Contains(t, m.err, "environment variable name")
}

func TestPresetFormSaveAndCancel(t *testing.T) {
	m := formKey(newPresetForm(context.Background(), testForm(), nil), kt(tea.KeyCtrlS))
	assert.True(t, m.saved)
	assert.Empty(t, m.View(), "nothing stays on screen")
	for _, k := range []tea.KeyMsg{kt(tea.KeyEsc), kt(tea.KeyCtrlC)} {
		m = formKey(newPresetForm(context.Background(), testForm(), nil), k)
		assert.False(t, m.saved)
		assert.True(t, m.done)
	}
}

func TestPresetFormView(t *testing.T) {
	m := newPresetForm(context.Background(), testForm(), nil)
	m = formKey(m, kt(tea.KeyDown))
	requireGolden(t, "presetform", m.View())
}

// The real program, fed keys on a pipe: ctrl-s returns the chosen values and
// draws no alternate screen; Esc cancels.
func TestEditPresetFormRunsInline(t *testing.T) {
	run := func(keys string) (app.PresetForm, string, error) {
		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		f, err := EditPresetForm(ctx, strings.NewReader(keys), &out, testForm(), nil)
		return f, out.String(), err
	}
	f, out, err := run("\x1b[B\x1b[C\x13")
	require.NoError(t, err)
	assert.Equal(t, "qa", f.Rows[1].Value)
	assert.NotContains(t, out, "\x1b[?1049h")

	_, _, err = run("\x03")
	assert.ErrorIs(t, err, context.Canceled)
}

func testEditForm() app.PresetForm {
	return app.PresetForm{Name: "deploy-qa", Plan: "PROJ-DEPLOY", Path: "~/bam/presets/proj.yaml", Editing: true,
		Rows: []app.FormRow{
			{Name: "ENV", Value: "qa", Orig: "qa", InPreset: true, Options: []string{"qa", "prod"}, OrigOptions: []string{"qa", "prod"}, Choices: []string{"qa", "prod"}},
			{Name: "REGION", Value: "eu", Orig: "eu", Plan: "eu", Choices: []string{"eu"}},
		},
		Fields: []app.FormField{
			{Key: "name", Label: "name", Value: "deploy-qa", Orig: "deploy-qa"},
			{Key: "timeout", Label: "timeout", Value: "", Orig: ""},
		}}
}

// An arrow on a removed variable brings it back with the value it steps to,
// so the save writes that value instead of dropping it.
func TestPresetEditFormArrowRestoresARemovedVariable(t *testing.T) {
	m := newPresetForm(context.Background(), testEditForm(), nil)
	m = formKey(m, rk("x"), kt(tea.KeyRight))
	assert.False(t, m.form.Rows[0].Removed)
	assert.Equal(t, "prod", m.form.Rows[0].Value)
	e := m.form.Edit()
	assert.Empty(t, e.Unset)
	require.Len(t, e.Set, 1)
	assert.Equal(t, "prod", e.Set[0].Value)
}

// A setting retyped as the same value, a timeout in another unit or a list
// with other spacing reads unchanged on its row, as it does in the footer.
func TestPresetEditFormRowNoteAgreesWithTheFooterOnRetypedSettings(t *testing.T) {
	f := testEditForm()
	f.Fields = append(f.Fields, app.FormField{Key: "required", Label: "required", Value: "ENV, REGION", Orig: "ENV, REGION"})
	f.Fields[1].Value, f.Fields[1].Orig = "45m", "45m"
	m := newPresetForm(context.Background(), f, nil)
	m.form.Fields[1].Value = "45m0s"
	m.form.Fields[2].Value = "ENV,REGION"
	v := m.View()
	assert.Contains(t, v, "nothing changed yet")
	assert.NotRegexp(t, `(?m)changed$`, v, "no row ends in changed")
	assert.NotRegexp(t, `45m0s\s+changed|ENV,REGION\s+changed`, v)
	m.form.Fields[2].Value = "ENV"
	assert.Regexp(t, `ENV\s+changed`, m.View())
}

// x removes a saved variable and again restores it; o edits its options;
// enter on a setting types its text; a bad timeout keeps the form open.
func TestPresetEditFormKeys(t *testing.T) {
	m := newPresetForm(context.Background(), testEditForm(), nil)
	assert.Contains(t, m.View(), "edit preset deploy-qa")
	assert.Contains(t, m.View(), "nothing changed yet")

	m = formKey(m, rk("x"))
	assert.True(t, m.form.Rows[0].Removed)
	assert.Contains(t, m.View(), "removed: the plan's own value applies")
	m = formKey(m, rk("x"))
	assert.False(t, m.form.Rows[0].Removed)

	m = formKey(m, rk("o"))
	assert.True(t, m.editing)
	m = formKey(m, kt(tea.KeyEnd), rk(","), rk("d"), rk("e"), rk("v"), kt(tea.KeyEnter))
	assert.Equal(t, []string{"qa", "prod", "dev"}, m.form.Rows[0].Options)
	assert.Equal(t, []string{"qa", "prod", "dev"}, m.form.Rows[0].Choices)

	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyEnter), rk("s"), rk("o"), kt(tea.KeyEnter))
	assert.Equal(t, "so", m.form.Fields[1].Value)
	m = formKey(m, kt(tea.KeyCtrlS))
	assert.False(t, m.saved, "a timeout that is no duration is refused while the form is open")
	assert.Contains(t, m.View(), "not a duration")

	m = formKey(m, kt(tea.KeyEnter), kt(tea.KeyBackspace), kt(tea.KeyBackspace), rk("4"), rk("5"), rk("m"), kt(tea.KeyEnter))
	m = formKey(m, kt(tea.KeyCtrlS))
	assert.True(t, m.saved)
	assert.Equal(t, "45m", m.form.Fields[1].Value)
}

// x and o are the edit form's; the add form types nothing on them.
func TestPresetAddFormIgnoresEditKeys(t *testing.T) {
	m := newPresetForm(context.Background(), testForm(), nil)
	m = formKey(m, rk("x"), rk("o"))
	assert.False(t, m.editing)
	assert.False(t, m.form.Rows[0].Removed)
}

// A saved literal secret is never drawn, and enter opens an empty input, so
// the value cannot leak to the screen or be saved back by accident.
func TestPresetEditFormHidesALiteralSecret(t *testing.T) {
	f := testEditForm()
	f.Rows = append(f.Rows, app.FormRow{Name: "api_secret", Value: "hunter2", Orig: "hunter2", InPreset: true, Secret: true, Literal: true})
	m := newPresetForm(context.Background(), f, nil)
	assert.NotContains(t, m.View(), "hunter2")
	assert.Contains(t, m.View(), "********")
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyEnter))
	assert.Empty(t, m.input.Value())
	assert.NotContains(t, m.View(), "hunter2")
	m = formKey(m, rk("A"), rk("P"), rk("I"), kt(tea.KeyEnter))
	assert.Equal(t, "${API}", m.form.Rows[2].Value)
	assert.Contains(t, m.View(), "${API}", "the name just set is shown")
	assert.NotContains(t, m.View(), "hunter2")
}

// Removing a variable both files hold leaves the other file's value in force,
// and the row says so.
func TestPresetEditFormSaysWhoseValueAppliesAfterARemoval(t *testing.T) {
	f := testEditForm()
	f.Held = app.Held{Only: map[string]string{}, Also: map[string]string{"variables.ENV": "low.yaml"}}
	m := newPresetForm(context.Background(), f, nil)
	m = formKey(m, rk("x"))
	assert.Contains(t, m.View(), "removed here: the value in low.yaml applies")
	assert.NotContains(t, m.View(), "the plan's own value applies")
}

// Clearing a setting both files hold leaves the other file's value in force,
// and the row says so, as it does for a removed variable.
func TestPresetEditFormSaysWhichOtherFileValueAppliesAfterClearing(t *testing.T) {
	f := testEditForm()
	f.Fields[1].Value, f.Fields[1].Orig = "45m", "45m"
	f.Rows[0].OrigOptions = []string{"qa", "prod"}
	f.Held = app.Held{Only: map[string]string{}, Also: map[string]string{"timeout": "low.yaml", "options.ENV": "low.yaml"}}
	m := newPresetForm(context.Background(), f, nil)
	assert.NotContains(t, m.View(), "applies")

	m.form.Fields[1].Value = ""
	m.form.Rows[0].Options = nil
	var timeoutRow, envRow string
	for _, l := range strings.Split(m.View(), "\n") {
		switch {
		case strings.Contains(l, "timeout"):
			timeoutRow = l
		case strings.Contains(l, "ENV"):
			envRow = l
		}
	}
	assert.Contains(t, timeoutRow, "cleared here: the value in low.yaml applies")
	assert.Contains(t, envRow, "options cleared here: the value in low.yaml applies")
}

func repoEditForm(read func(context.Context, string) ([]app.RepoBranch, error)) app.PresetForm {
	f := testEditForm()
	f.Branch = "release-1"
	f.Fields = []app.FormField{
		{Key: "name", Label: "name", Value: "deploy-qa", Orig: "deploy-qa"},
		{Key: "branch", Label: "branch", Value: "release-1", Orig: "release-1"},
		{Key: "repositories.api", Label: "repository api", Value: "main", Orig: "main"},
		{Key: "timeout", Label: "timeout", Value: "", Orig: ""},
	}
	f.ReadRepos = read
	return f
}

// x on a repository removes it and again brings it back; a adds one typed as
// NAME=BRANCH and refuses text that is not.
func TestPresetEditFormRemovesAndAddsRepositories(t *testing.T) {
	m := newPresetForm(context.Background(), repoEditForm(nil), nil)
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), rk("x"))
	assert.True(t, m.form.Fields[2].Removed)
	assert.Contains(t, m.View(), "repository api")
	assert.Contains(t, m.View(), "removed")
	m = formKey(m, rk("x"))
	assert.False(t, m.form.Fields[2].Removed)

	m = formKey(m, rk("a"))
	assert.True(t, m.adding)
	assert.Contains(t, m.View(), "new repository, NAME=BRANCH")
	m = formKey(m, rk("d"), rk("o"), kt(tea.KeyEnter))
	assert.Contains(t, m.err, "NAME=BRANCH", "text without a branch is refused")
	assert.True(t, m.adding)
	m = formKey(m, rk("c"), rk("s"), rk("="), rk("m"), kt(tea.KeyEnter))
	assert.False(t, m.adding)
	assert.Equal(t, "repositories.docs", m.form.Fields[3].Key)
	assert.Equal(t, "m", m.form.Fields[3].Value)
	assert.Contains(t, m.View(), "added")
}

// The footer counts each repository removed, added or changed, as the save
// lists them, not all of them as one change.
func TestPresetEditFormCountsEachRepositoryChange(t *testing.T) {
	m := newPresetForm(context.Background(), repoEditForm(nil), nil)
	assert.Contains(t, m.View(), "nothing changed yet")
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), rk("x"))
	assert.Contains(t, m.View(), "1 change(s) to save")
	m = formKey(m, rk("a"), rk("c"), rk("s"), rk("="), rk("m"), kt(tea.KeyEnter))
	assert.Contains(t, m.View(), "2 change(s) to save")
}

// A new branch reads the repositories Bamboo lists for it and puts them in
// place of the old ones; an answer for a branch typed over meanwhile is ignored.
func TestPresetEditFormReadsRepositoriesWhenTheBranchChanges(t *testing.T) {
	var asked []string
	read := func(_ context.Context, branch string) ([]app.RepoBranch, error) {
		asked = append(asked, branch)
		return []app.RepoBranch{{Name: "scripts", Branch: "develop"}}, nil
	}
	m := newPresetForm(context.Background(), repoEditForm(read), nil)
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyEnter), kt(tea.KeyCtrlU), rk("r"), rk("2"))
	n, cmd := m.Update(kt(tea.KeyEnter))
	m = n.(presetFormModel)
	require.NotNil(t, cmd, "a changed branch reads the repositories")
	assert.Contains(t, m.View(), "reading the repositories of r2")
	msg := cmd()
	assert.Equal(t, []string{"r2"}, asked)

	stale, _ := m.Update(reposMsg{branch: "other", repos: []app.RepoBranch{{Name: "x", Branch: "y"}}})
	assert.Len(t, stale.(presetFormModel).form.Fields, 4, "an answer for another branch changes nothing")

	n, _ = m.Update(msg)
	m = n.(presetFormModel)
	assert.NotContains(t, m.View(), "reading the repositories")
	assert.True(t, m.form.Fields[2].Removed, "api is not listed for r2")
	assert.Equal(t, "repositories.scripts", m.form.Fields[3].Key)
	assert.Equal(t, "develop", m.form.Fields[3].Value)

	// Typing the same branch again, or none, reads nothing.
	m2 := newPresetForm(context.Background(), repoEditForm(read), nil)
	m2 = formKey(m2, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyEnter))
	_, cmd = m2.Update(kt(tea.KeyEnter))
	assert.Nil(t, cmd)
}

func TestPresetEditFormShowsWhyRepositoriesCouldNotBeRead(t *testing.T) {
	read := func(context.Context, string) ([]app.RepoBranch, error) { return nil, assert.AnError }
	m := newPresetForm(context.Background(), repoEditForm(read), nil)
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyEnter), kt(tea.KeyCtrlU), rk("r"))
	n, cmd := m.Update(kt(tea.KeyEnter))
	n, _ = n.(presetFormModel).Update(cmd())
	m = n.(presetFormModel)
	assert.Contains(t, m.View(), "could not read the repositories of r")
	assert.False(t, m.form.Fields[2].Removed, "a failed read keeps the repositories as they were")
}

// A repository both files hold says whose value applies once it is removed here.
func TestPresetEditFormSaysWhoseRepositoryAppliesAfterARemoval(t *testing.T) {
	f := repoEditForm(nil)
	f.Held = app.Held{Only: map[string]string{}, Also: map[string]string{"repositories.api": "low.yaml"}}
	m := newPresetForm(context.Background(), f, nil)
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), rk("x"))
	assert.Contains(t, m.View(), "removed here: the value in low.yaml applies")
}

// A secret variable's options are values like its own: never drawn.
func TestPresetEditFormHidesTheOptionsOfASecret(t *testing.T) {
	f := testEditForm()
	f.Rows = append(f.Rows, app.FormRow{Name: "api_secret", Value: "${API}", Orig: "${API}", InPreset: true, Secret: true,
		Options: []string{"hunter2", "s3cret"}, OrigOptions: []string{"hunter2", "s3cret"}})
	m := newPresetForm(context.Background(), f, nil)
	assert.NotContains(t, m.View(), "hunter2")
	assert.NotContains(t, m.View(), "s3cret")
	assert.Contains(t, m.View(), "options: ***", "the row is cut at the screen edge, so only the start shows")
}

// Saving while the repositories of a new branch are still being read would
// keep the old ones, so ctrl-s waits for the answer.
func TestPresetEditFormDoesNotSaveWhileReadingRepositories(t *testing.T) {
	read := func(context.Context, string) ([]app.RepoBranch, error) { return nil, nil }
	m := newPresetForm(context.Background(), repoEditForm(read), nil)
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyEnter), kt(tea.KeyCtrlU), rk("r"), rk("2"))
	n, cmd := m.Update(kt(tea.KeyEnter))
	m = n.(presetFormModel)
	n, quit := m.Update(kt(tea.KeyCtrlS))
	m = n.(presetFormModel)
	assert.Nil(t, quit)
	assert.False(t, m.saved)
	assert.Contains(t, m.View(), "still reading the repositories of r2")
	n, _ = m.Update(cmd())
	m = n.(presetFormModel)
	_, quit = m.Update(kt(tea.KeyCtrlS))
	assert.NotNil(t, quit, "saves once the answer is in")
}

// The answer for a branch can arrive while a setting after the repositories is
// being typed: it waits until the edit ends, so the text lands in that setting,
// and the cursor stays on it although the answer adds a repository before it.
func TestPresetEditFormHoldsTheRepositoriesAnswerWhileTyping(t *testing.T) {
	read := func(context.Context, string) ([]app.RepoBranch, error) {
		return []app.RepoBranch{{Name: "api", Branch: "main"}, {Name: "scripts", Branch: "develop"}}, nil
	}
	m := newPresetForm(context.Background(), repoEditForm(read), nil)
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyEnter), kt(tea.KeyCtrlU), rk("r"), rk("2"))
	n, cmd := m.Update(kt(tea.KeyEnter))
	m = n.(presetFormModel)
	require.NotNil(t, cmd)
	msg := cmd()
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyEnter), kt(tea.KeyCtrlU), rk("4"), rk("5"), rk("m"))
	require.Equal(t, "field:timeout", m.lineID())
	before := len(m.form.Fields)
	n, _ = m.Update(msg)
	m = n.(presetFormModel)
	assert.Equal(t, before, len(m.form.Fields), "the answer waits while a setting is typed")
	m = formKey(m, kt(tea.KeyEnter))
	assert.Equal(t, before+1, len(m.form.Fields), "the answer applies once the edit ends")
	for _, fl := range m.form.Fields {
		if fl.Key == "timeout" {
			assert.Equal(t, "45m", fl.Value, "the text stays in the setting it was typed for")
		} else {
			assert.NotEqual(t, "45m", fl.Value, fl.Key)
		}
	}
	assert.Equal(t, "field:timeout", m.lineID(), "the cursor stays on the setting it was on")
}

// While the repositories are being read, adding one and changing one are
// refused, since the answer would replace what was typed.
func TestPresetEditFormRefusesRepositoryEditsWhileReading(t *testing.T) {
	read := func(context.Context, string) ([]app.RepoBranch, error) { return nil, nil }
	m := newPresetForm(context.Background(), repoEditForm(read), nil)
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyEnter), kt(tea.KeyCtrlU), rk("r"), rk("2"), kt(tea.KeyEnter))
	require.Equal(t, "field:repositories.api", m.lineID())
	require.NotEmpty(t, m.reading)
	for _, k := range []tea.KeyMsg{kt(tea.KeyEnter), rk("x"), rk("a")} {
		m = formKey(m, k)
		assert.False(t, m.editing, "%v", k)
		assert.False(t, m.form.Fields[2].Removed)
		assert.Contains(t, m.err, "still reading the repositories of r2")
	}

	// a adds a repository from any line, so it is refused on one that is not a repository too.
	m = formKey(m, kt(tea.KeyDown))
	require.Equal(t, "field:timeout", m.lineID())
	m = formKey(m, rk("a"))
	assert.False(t, m.adding, "a is refused on the timeout while reading")
	assert.False(t, m.editing)
	assert.Contains(t, m.err, "still reading the repositories of r2")

	// x on a variable row is not a repository edit, so it still works.
	m = formKey(m, kt(tea.KeyUp), kt(tea.KeyUp), kt(tea.KeyUp), kt(tea.KeyUp), kt(tea.KeyUp), kt(tea.KeyUp), kt(tea.KeyUp))
	require.NotNil(t, m.row())
	was := m.row().Removed
	m = formKey(m, rk("x"))
	assert.NotEqual(t, was, m.row().Removed, "x on a variable row works while reading")
}

// Clearing the branch before the answer arrives drops the answer.
func TestPresetEditFormDropsTheAnswerForAClearedBranch(t *testing.T) {
	read := func(context.Context, string) ([]app.RepoBranch, error) {
		return []app.RepoBranch{{Name: "scripts", Branch: "develop"}}, nil
	}
	m := newPresetForm(context.Background(), repoEditForm(read), nil)
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyEnter), kt(tea.KeyCtrlU), rk("r"), rk("2"))
	n, cmd := m.Update(kt(tea.KeyEnter))
	m = n.(presetFormModel)
	msg := cmd()
	m = formKey(m, kt(tea.KeyUp), kt(tea.KeyEnter), kt(tea.KeyCtrlU), kt(tea.KeyEnter))
	require.Equal(t, "", m.branchValue())
	before := len(m.form.Fields)
	n, _ = m.Update(msg)
	m = n.(presetFormModel)
	assert.Equal(t, before, len(m.form.Fields), "the old branch's repositories are not applied")
	assert.Equal(t, "", m.reading)
}
