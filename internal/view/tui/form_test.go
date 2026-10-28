package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

func sampleTarget() *config.ResolvedTarget {
	return &config.ResolvedTarget{Name: "provision-lab", Target: config.Target{
		Plan:     "PROJ-PROV",
		Branch:   "develop",
		Defaults: config.StringMap{"cluster_type": "k8s"},
		Options:  config.StringListMap{"cluster_type": {"k8s", "dcos"}},
		Required: []string{"cluster_name"},
	}}
}

func sampleRef() app.PlanRef {
	return app.PlanRef{PlanKey: "PROJ-PROV12", MasterKey: "PROJ-PROV", Branch: "develop", Target: sampleTarget()}
}

func sampleBase() app.VarSet {
	return app.VarSet{
		DeclaredKnown: true,
		Declared:      map[string]bool{"cluster_name": true, "cluster_type": true, "ssh_key": true},
		Vars: []app.ResolvedVar{
			{Name: "cluster_name", Declared: true, Source: "plan"},
			{Name: "cluster_type", Value: "k8s", PlanValue: "k8s", Source: "preset", Declared: true},
			{Name: "ssh_key", Value: app.MaskedDisplay, PlanValue: app.MaskedDisplay, Source: "plan", Declared: true, Secret: true},
		},
	}
}

func TestBuildFieldsCarriesSourceRequiredAndOptions(t *testing.T) {
	fs := buildFields(sampleBase(), sampleRef())
	require.Len(t, fs, 3)

	require.Equal(t, "cluster_name", fs[0].Name)
	require.True(t, fs[0].Required)
	require.Empty(t, fs[0].Options)

	require.Equal(t, "cluster_type", fs[1].Name)
	require.Equal(t, []string{"k8s", "dcos"}, fs[1].Options)
	require.False(t, fs[1].Required)
	require.Equal(t, "preset", fs[1].Source)

	require.Equal(t, "ssh_key", fs[2].Name)
	require.True(t, fs[2].Secret)
}

// TestBuildFieldsAddsARequiredNameThePlanDoesNotDeclare, so it can be filled
// in rather than being invisible and impossible to satisfy.
func TestBuildFieldsAddsARequiredNameThePlanDoesNotDeclare(t *testing.T) {
	ref := sampleRef()
	ref.Target.Required = append(ref.Target.Required, "ticket")
	names := fieldNames(buildFields(sampleBase(), ref))
	require.Contains(t, names, "ticket")
}

// TestSecretFieldsStartEmpty: a secret is never prefilled, so what is on
// screen is never a value the user did not type.
func TestSecretFieldsStartEmpty(t *testing.T) {
	for _, f := range buildFields(sampleBase(), sampleRef()) {
		if f.Secret {
			require.Equal(t, "", f.Value)
			require.False(t, f.Touched)
		}
	}
}

// TestFlagsSendsOnlyWhatWasTyped. An untouched field's value already sits in
// the base that ValidateVars is given, so resending it would only produce a
// spurious undeclared-name warning; an untouched secret therefore sends
// nothing, and the plan's or the environment's value stands.
func TestFlagsSendsOnlyWhatWasTyped(t *testing.T) {
	f := formState{fields: buildFields(sampleBase(), sampleRef())}
	require.Empty(t, f.flags())

	f.fields[0].Value, f.fields[0].Touched = "beta", true
	require.Equal(t, []string{"cluster_name=beta"}, f.flags())

	f.fields[2].Value, f.fields[2].Touched = "typed", true
	require.Contains(t, f.flags(), "ssh_key=typed")
}

// TestFlagsKeepsAnEmptyTypedValue: clearing a field on purpose is a value.
func TestFlagsKeepsAnEmptyTypedValue(t *testing.T) {
	f := formState{fields: buildFields(sampleBase(), sampleRef())}
	f.fields[1].Value, f.fields[1].Touched = "", true
	require.Equal(t, []string{"cluster_type="}, f.flags())
}

func fieldNames(fs []formField) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}

func formModel() Model {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.screen = screenForm
	m.form = formState{ref: sampleRef(), target: "provision-lab", base: sampleBase(),
		fields: buildFields(sampleBase(), sampleRef())}
	m.form.fields[0].Value, m.form.fields[0].Touched = "beta", true
	return m
}

func TestFormHeaderNamesTheTargetPlanAndBranch(t *testing.T) {
	v := formModel().View()
	require.Contains(t, v, "provision-lab")
	require.Contains(t, v, "PROJ-PROV12")
	require.Contains(t, v, "develop")
}

func TestFormShowsSourcesAndConstraints(t *testing.T) {
	v := formModel().View()
	require.Contains(t, v, "required")
	require.Contains(t, v, "k8s | dcos")
}

// TestFormNeverShowsASecretValue.
func TestFormNeverShowsASecretValue(t *testing.T) {
	m := formModel()
	m.form.fields[2].Value, m.form.fields[2].Touched = "s3cret", true
	v := m.View()
	require.NotContains(t, v, "s3cret")
	require.Contains(t, v, app.MaskedDisplay)
}

func TestFormHidesTheLeftColumn(t *testing.T) {
	require.NotContains(t, formModel().View(), "1 Plans")
}

func TestFormCountsWhatChanged(t *testing.T) {
	require.Contains(t, formModel().View(), "1 of 3 changed")
}

func TestFormWhileLoadingSaysSo(t *testing.T) {
	m := formModel()
	m.form.loading, m.form.fields = true, nil
	require.Contains(t, m.View(), "loading")
}

func TestFormGolden(t *testing.T) { requireGolden(t, "form-80x24", formModel().View()) }

func TestROpensTheFormForAPreset(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m, _ = send(m, presetsLoadedMsg{Targets: []app.TargetInfo{{Name: "smoke", Plan: "PROJ-PROV", Branch: "develop"}}})
	m.focus = focusPresets

	m, cmd := send(m, mkKey("R"))
	require.Equal(t, screenForm, m.screen)
	require.True(t, m.form.loading)
	require.NotNil(t, cmd)
	v := m.View()
	require.Contains(t, v, "PROJ-PROV · branch develop · preset smoke", "the header names what is loading")

	msg, ok := cmd().(formLoadedMsg)
	require.True(t, ok, "got %T", cmd())
	require.Equal(t, "PROJ-PROV12", msg.Ref.PlanKey, "the preset's branch is resolved")
	require.Equal(t, "smoke", msg.Target)
}

func TestROnAPlanOpensAFormWithNoTargetRules(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "PROJ-BUILD"}}})
	m.focus = focusPlans

	m, cmd := send(m, mkKey("R"))
	require.NotNil(t, cmd)
	require.Contains(t, m.View(), "PROJ-BUILD", "the header names the plan while it loads")
	msg, ok := cmd().(formLoadedMsg)
	require.True(t, ok, "got %T", cmd())
	require.Equal(t, "PROJ-BUILD", msg.Ref.PlanKey)
	require.Nil(t, msg.Ref.Target)
	require.Equal(t, "", msg.Target)
}

func TestFormLoadedFillsTheFields(t *testing.T) {
	m := goldenModel(80, 24)
	m.screen, m.form.loading = screenForm, true
	m, _ = send(m, formLoadedMsg{Gen: m.formGen, Ref: sampleRef(), Target: "provision-lab", Base: sampleBase()})
	require.False(t, m.form.loading)
	require.Len(t, m.form.fields, 3)
	require.Equal(t, "provision-lab", m.form.target)
}

// TestEnterWhileLoadingIsIgnoredUntilTheFormArrives: onRevision() is true
// while the form is loading (fields nil, cursor 0), so enter must not start
// editing the hidden revision input; and once the fields arrive, the earlier
// enter must not have left editing on to swallow the next real keystroke.
func TestEnterWhileLoadingIsIgnoredUntilTheFormArrives(t *testing.T) {
	m := goldenModel(80, 24)
	m.screen, m.form.loading = screenForm, true

	m, _ = send(m, mkKey("enter"))
	require.False(t, m.form.editing, "enter while loading must not start editing")

	m, _ = send(m, formLoadedMsg{Gen: m.formGen, Ref: sampleRef(), Target: "provision-lab", Base: sampleBase()})
	require.False(t, m.form.editing, "the load must not inherit editing from the stray enter")
	for _, f := range m.form.fields {
		require.False(t, f.Touched, f.Name+" must not be touched")
	}
	require.Equal(t, "", m.form.fields[0].Value, "the first field's value must be unchanged")
}

func TestStaleFormLoadIsDropped(t *testing.T) {
	m := formModel()
	stale := m.formGen
	m.formGen++
	m, _ = send(m, formLoadedMsg{Gen: stale, Ref: sampleRef(), Target: "other", Base: sampleBase()})
	require.Equal(t, "provision-lab", m.form.target)
}

func TestEscLeavesTheForm(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen)
}

// TestROnNothingDoesNothing.
func TestROnNothingDoesNothing(t *testing.T) {
	m := New(Deps{})
	m.width, m.height = 80, 24
	m.screen = screenPlan
	m, cmd := send(m, mkKey("R"))
	require.Nil(t, cmd)
	require.Equal(t, screenPlan, m.screen)
}

// TestRIsNotReachableFromTheLogScreen: the log screen is for reading.
func TestRIsNotReachableFromTheLogScreen(t *testing.T) {
	m := logModel()
	m, cmd := send(m, mkKey("R"))
	require.Nil(t, cmd)
	require.Equal(t, screenLogs, m.screen)
}

func TestTabMovesBetweenFields(t *testing.T) {
	m := formModel()
	require.Equal(t, 0, m.form.cursor)
	m, _ = send(m, mkKey("tab"))
	require.Equal(t, 1, m.form.cursor)
	m, _ = send(m, mkKey("shift+tab"))
	require.Equal(t, 0, m.form.cursor)
	m, _ = send(m, mkKey("shift+tab"))
	require.Equal(t, 3, m.form.cursor, "moving back from the first field wraps to the revision row")
}

func TestJAndKMoveBetweenFieldsToo(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("j"))
	require.Equal(t, 1, m.form.cursor)
	m, _ = send(m, mkKey("k"))
	require.Equal(t, 0, m.form.cursor)
}

func TestEnterEditsAFreeTextFieldAndAcceptsIt(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.form.editing)
	require.Equal(t, "beta", m.form.input.Value(), "editing starts from the current value")

	for _, r := range "gamma" {
		m, _ = send(m, mkKey(string(r)))
	}
	m, _ = send(m, mkKey("enter"))
	require.False(t, m.form.editing)
	require.Equal(t, "betagamma", m.form.fields[0].Value)
	require.True(t, m.form.fields[0].Touched)
}

// TestEnterOnAnOptionsFieldCyclesRatherThanEdits: the commonest rejection
// cannot be typed at all.
func TestEnterOnAnOptionsFieldCyclesRatherThanEdits(t *testing.T) {
	m := formModel()
	m.form.cursor = 1
	m, _ = send(m, mkKey("enter"))
	require.False(t, m.form.editing)
	require.Equal(t, "dcos", m.form.fields[1].Value)
	require.True(t, m.form.fields[1].Touched)

	m, _ = send(m, mkKey("enter"))
	require.Equal(t, "k8s", m.form.fields[1].Value, "cycling wraps")
}

// TestEscWhileEditingLeavesTheFieldNotTheForm.
func TestEscWhileEditingLeavesTheFieldNotTheForm(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, mkKey("esc"))
	require.False(t, m.form.editing)
	require.Equal(t, screenForm, m.screen)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen)
}

// TestEscWhileEditingKeepsTheOldValue.
func TestEscWhileEditingKeepsTheOldValue(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("enter"))
	for _, r := range "zzz" {
		m, _ = send(m, mkKey(string(r)))
	}
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, "beta", m.form.fields[0].Value)
}

// TestTypingQWhileEditingDoesNotQuit.
func TestTypingQWhileEditingDoesNotQuit(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, mkKey("q"))
	require.Equal(t, screenForm, m.screen)
	require.True(t, m.form.editing)
	require.Contains(t, m.form.input.Value(), "q")
}

// TestASecretFieldNeverEchoesWhatIsTyped.
func TestASecretFieldNeverEchoesWhatIsTyped(t *testing.T) {
	m := formModel()
	m.form.cursor = 2
	m, _ = send(m, mkKey("enter"))
	for _, r := range "s3cret" {
		m, _ = send(m, mkKey(string(r)))
	}
	require.NotContains(t, m.View(), "s3cret")

	m, _ = send(m, mkKey("enter"))
	require.Equal(t, "s3cret", m.form.fields[2].Value)
	require.True(t, m.form.fields[2].Touched)
	require.NotContains(t, m.View(), "s3cret")
}

func TestAnEmptyRequiredFieldIsMarkedAndBlocksRun(t *testing.T) {
	m := formModel()
	m.form.fields[0].Value, m.form.fields[0].Touched = "", true
	m.revalidate()
	require.Contains(t, m.form.fields[0].Err, "required")
	require.False(t, m.canRun())
}

func TestFillingTheFieldClearsTheMark(t *testing.T) {
	m := formModel()
	m.form.fields[0].Value, m.form.fields[0].Touched = "", true
	m.revalidate()
	require.NotEmpty(t, m.form.fields[0].Err)

	m.form.fields[0].Value = "beta"
	m.revalidate()
	require.Empty(t, m.form.fields[0].Err)
	require.True(t, m.canRun())
}

// TestAnUnsetEnvReferenceNamesTheVariableAndBlocksRun.
func TestAnUnsetEnvReferenceNamesTheVariableAndBlocksRun(t *testing.T) {
	base := sampleBase()
	base.Vars = append(base.Vars, app.ResolvedVar{Name: "token", Value: "${LAB_TOKEN}", Source: "preset"})
	ref := sampleRef()
	ref.Target.Defaults = config.StringMap{"cluster_type": "k8s", "token": "${LAB_TOKEN}"}

	m := formModel()
	m.deps.Getenv = func(string) string { return "" }
	m.form.base, m.form.ref = base, ref
	m.form.fields = buildFields(base, ref)
	m.revalidate()

	// The message names the variable, so it lands on that field's row rather
	// than only in the footer.
	var note string
	for _, f := range m.form.fields {
		if f.Name == "token" {
			note = f.Err
		}
	}
	require.Equal(t, "unset ${ENV}", note)
	require.False(t, m.canRun())
}

// TestWarningsDoNotBlock, exactly as they do not block bam run.
func TestWarningsDoNotBlock(t *testing.T) {
	m := formModel()
	m.form.fields = append(m.form.fields, formField{Name: "typo_name", Value: "x", Touched: true})
	m.revalidate()
	require.True(t, m.canRun())
	require.NoError(t, m.form.err)
}

// TestCyclingAnOptionRevalidates: the feedback follows the keystroke.
func TestCyclingAnOptionRevalidates(t *testing.T) {
	m := formModel()
	m.form.fields[0].Value, m.form.fields[0].Touched = "", true
	m.form.cursor = 1
	m, _ = send(m, mkKey("enter"))
	require.Contains(t, m.form.fields[0].Err, "required", "the required mark survives a cycle elsewhere")
}

// TestAcceptingAnEditRevalidates.
func TestAcceptingAnEditRevalidates(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("enter"))
	for i := 0; i < len("beta"); i++ {
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m, _ = send(m, mkKey("enter"))
	require.Contains(t, m.form.fields[0].Err, "required")
}

// TestNoSecretEverReachesTheScreen fills every secret field with a sentinel
// and checks it appears nowhere a person or a terminal recording could see
// it. It is the test that catches a future renderer that forgets.
func TestNoSecretEverReachesTheScreen(t *testing.T) {
	const sentinel = "correct-horse-battery-staple"

	m := formModel()
	for i := range m.form.fields {
		if m.form.fields[i].Secret {
			m.form.fields[i].Value, m.form.fields[i].Touched = sentinel, true
		}
	}
	m.revalidate()

	surfaces := map[string]string{
		"the form":       m.View(),
		"the form body":  m.formBody(76, 20),
		"the status bar": m.statusBar(80),
	}
	for name, s := range surfaces {
		require.NotContains(t, s, sentinel, "a secret reached %s", name)
	}

	// And while it is being typed.
	m.form.cursor = 2
	m, _ = send(m, mkKey("enter"))
	for _, r := range sentinel {
		m, _ = send(m, mkKey(string(r)))
	}
	require.NotContains(t, m.View(), sentinel, "a secret reached the screen while being typed")

	// The value is still carried, or the run would send the wrong thing.
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, sentinel, m.form.fields[2].Value)
	require.Contains(t, m.form.flags(), "ssh_key="+sentinel)
}

func TestCtrlROnAValidFormTriggers(t *testing.T) {
	m := formModel()
	m.revalidate()
	require.True(t, m.canRun())

	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.NotNil(t, cmd)
	msg, ok := cmd().(triggeredMsg)
	require.True(t, ok, "got %T", cmd())
	require.NotEmpty(t, msg.Build.Key)
}

func TestCtrlRRefusesWhileAnErrorStands(t *testing.T) {
	m := formModel()
	m.form.fields[0].Value, m.form.fields[0].Touched = "", true
	m.revalidate()

	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.Nil(t, cmd)
	require.Equal(t, screenForm, m.screen, "the form stays open")
}

// TestATriggerSendsOnlyChangedVariables, the same set bam run sends.
func TestATriggerSendsOnlyChangedVariables(t *testing.T) {
	m := formModel()
	m.revalidate()
	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.NotNil(t, cmd)
	cmd()

	f := m.svc.P.(*fake.Provider)
	require.Len(t, f.Triggered, 1)
	require.Equal(t, map[string]string{"cluster_name": "beta"}, f.Triggered[0].Variables,
		"cluster_type equals the plan's value, so it is not sent")
}

// TestASuccessfulRunOpensAndWatchesTheNewBuild.
func TestASuccessfulRunOpensAndWatchesTheNewBuild(t *testing.T) {
	m := formModel()
	m, _ = send(m, triggeredMsg{Gen: m.formGen,
		Build: provider.Build{Key: "PROJ-PROV12-9", PlanKey: "PROJ-PROV12", Number: 9}})
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, focusStages, m.focus)
	require.NotNil(t, m.detail)
	require.Equal(t, "PROJ-PROV12-9", m.detail.Key)
	require.NotNil(t, m.watchCancel, "the new build is watched")
	m.stopWatch()
}

// TestTheQueuedNoticeEndsWhenTheBuildLeavesTheQueue: "queued KEY" is news
// only while the build waits. Once the watch sees it run, the detail panel
// shows the state and the status bar goes back to server, version and user.
func TestTheQueuedNoticeEndsWhenTheBuildLeavesTheQueue(t *testing.T) {
	m := formModel()
	b := provider.Build{Key: "PROJ-PROV12-9", PlanKey: "PROJ-PROV12", Number: 9, State: provider.StateQueued}
	m, _ = send(m, triggeredMsg{Gen: m.formGen, Build: b})
	defer m.stopWatch()
	require.Equal(t, "queued PROJ-PROV12-9", m.status)

	m, _ = send(m, watchEventMsg{Gen: m.watchGen, Event: app.Event{Type: app.EventState, Build: b, State: b.State}})
	require.Equal(t, "queued PROJ-PROV12-9", m.status, "still queued")

	b.State = provider.StateRunning
	m, _ = send(m, watchEventMsg{Gen: m.watchGen, Event: app.Event{Type: app.EventState, Build: b, State: b.State}})
	require.Empty(t, m.status)
	require.NotContains(t, m.statusBar(80), "queued")
}

// TestTheQueuedNoticeEndsWhenTheBuildFinishesStraightFromTheQueue.
func TestTheQueuedNoticeEndsWhenTheBuildFinishesStraightFromTheQueue(t *testing.T) {
	m := formModel()
	b := provider.Build{Key: "PROJ-PROV12-9", PlanKey: "PROJ-PROV12", Number: 9, State: provider.StateQueued}
	m, _ = send(m, triggeredMsg{Gen: m.formGen, Build: b})
	defer m.stopWatch()

	b.State = provider.StateNotBuilt
	m, _ = send(m, watchEventMsg{Gen: m.watchGen, Event: app.Event{Type: app.EventDone, Build: b, State: b.State}})
	require.Empty(t, m.status)
}

// TestAnotherNoticeSurvivesTheWatch: only the queued notice belongs to the
// watch; a notice the user caused since then is not the watch's to clear.
func TestAnotherNoticeSurvivesTheWatch(t *testing.T) {
	m := formModel()
	b := provider.Build{Key: "PROJ-PROV12-9", PlanKey: "PROJ-PROV12", Number: 9, State: provider.StateQueued}
	m, _ = send(m, triggeredMsg{Gen: m.formGen, Build: b})
	defer m.stopWatch()
	m.status = "copied to clipboard (osc 52)"

	b.State = provider.StateRunning
	m, _ = send(m, watchEventMsg{Gen: m.watchGen, Event: app.Event{Type: app.EventState, Build: b, State: b.State}})
	require.Equal(t, "copied to clipboard (osc 52)", m.status)
}

// TestAFailedTriggerKeepsTheFormAndWhatWasTyped.
func TestAFailedTriggerKeepsTheFormAndWhatWasTyped(t *testing.T) {
	m := formModel()
	m, _ = send(m, errMsg{Err: errBoom, Where: "run"})
	require.Equal(t, screenForm, m.screen)
	require.Equal(t, "beta", m.form.fields[0].Value)
	require.Error(t, m.err)
}

// TestCtrlRCannotBeStruckWhileEditing.
func TestCtrlRCannotBeStruckWhileEditing(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("enter"))
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.Nil(t, cmd)
	require.True(t, m.form.editing)
	f := m.svc.P.(*fake.Provider)
	require.Empty(t, f.Triggered)
}

// TestStaleTriggerIsDropped.
func TestStaleTriggerIsDropped(t *testing.T) {
	m := formModel()
	stale := m.formGen
	m.formGen++
	m, _ = send(m, triggeredMsg{Gen: stale, Build: provider.Build{Key: "PROJ-PROV12-9"}})
	require.Equal(t, screenForm, m.screen)
	require.Nil(t, m.detail)
}

// TestAnUntypedMaskIsNeverSentBack: Bamboo reports a secret as ********, and
// sending that string back would overwrite the real secret with asterisks.
func TestAnUntypedMaskIsNeverSentBack(t *testing.T) {
	base := sampleBase()
	// A server that reports the mask without a plan value of its own.
	base.Vars[2].PlanValue = ""
	f := formState{fields: buildFields(base, sampleRef())}

	vs := app.VarSet{Vars: []app.ResolvedVar{
		{Name: "ssh_key", Value: app.MaskedDisplay, PlanValue: "", Declared: true},
	}}
	require.NotContains(t, f.stripUntypedMasks(vs).Changed(), "ssh_key")

	f.fields[2].Value, f.fields[2].Touched = "typed", true
	vs.Vars[0].Value = "typed"
	require.Equal(t, "typed", f.stripUntypedMasks(vs).Changed()["ssh_key"])
}

func TestDShowsWhatWouldBeSentAndReachesNoServer(t *testing.T) {
	m := formModel()
	m.revalidate()
	m, cmd := send(m, mkKey("d"))
	require.Equal(t, overlayDryRun, m.overlay)
	require.Nil(t, cmd, "a dry-run sends nothing")

	v := m.View()
	require.Contains(t, v, "cluster_name=beta")
	require.Empty(t, m.svc.P.(*fake.Provider).Triggered)
}

func TestDryRunMasksSecrets(t *testing.T) {
	m := formModel()
	m.form.fields[2].Value, m.form.fields[2].Touched = "s3cret", true
	m.revalidate()
	m, _ = send(m, mkKey("d"))
	require.NotContains(t, m.View(), "s3cret")
	require.Contains(t, m.View(), "ssh_key="+app.MaskedDisplay)
}

func TestDryRunSaysWhenNothingChanged(t *testing.T) {
	// A bare plan: nothing required, nothing typed, so nothing to send.
	m := formModel()
	base := app.VarSet{DeclaredKnown: true, Declared: map[string]bool{"debug": true},
		Vars: []app.ResolvedVar{{Name: "debug", Value: "false", PlanValue: "false", Source: "plan", Declared: true}}}
	ref := app.PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD"}
	m.form = formState{ref: ref, base: base, fields: buildFields(base, ref)}
	m.revalidate()

	m, _ = send(m, mkKey("d"))
	require.Contains(t, m.View(), "no variables")
}

func TestEscClosesTheDryRunAndKeepsTheForm(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("d"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, overlayNone, m.overlay)
	require.Equal(t, screenForm, m.screen)
}

func cancelModel() Model {
	m := goldenModel(80, 24)
	m.svc = testService()
	running := sampleBuild()
	running.State = provider.StateRunning
	// Cancel re-reads the build from the server, so the server's copy is the
	// one that has to be running.
	m.svc.P.(*fake.Provider).History["PROJ-BUILD"] = []provider.Build{running}
	m.builds.setItems([]provider.Build{running})
	m.focus = focusBuilds
	return m
}

func TestCAsksBeforeCancelling(t *testing.T) {
	m := cancelModel()
	m, cmd := send(m, mkKey("C"))
	require.Equal(t, overlayConfirm, m.overlay)
	require.Nil(t, cmd, "nothing is sent before the answer")
	require.Contains(t, m.confirm.Prompt, "PROJ-BUILD-44")
	require.Empty(t, m.svc.P.(*fake.Provider).Stopped)
}

func TestNoDoesNotCancel(t *testing.T) {
	m := cancelModel()
	m, _ = send(m, mkKey("C"))
	m, cmd := send(m, mkKey("n"))
	require.Equal(t, overlayNone, m.overlay)
	require.Nil(t, cmd)
	require.Empty(t, m.svc.P.(*fake.Provider).Stopped)
}

func TestEscDoesNotCancelEither(t *testing.T) {
	m := cancelModel()
	m, _ = send(m, mkKey("C"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, overlayNone, m.overlay)
	require.Empty(t, m.svc.P.(*fake.Provider).Stopped)
}

func TestYesCancels(t *testing.T) {
	m := cancelModel()
	m, _ = send(m, mkKey("C"))
	m, cmd := send(m, mkKey("y"))
	require.Equal(t, overlayNone, m.overlay)
	require.NotNil(t, cmd)
	cmd()
	require.Equal(t, []string{"PROJ-BUILD-44"}, m.svc.P.(*fake.Provider).Stopped)
}

func TestEnterAlsoConfirms(t *testing.T) {
	m := cancelModel()
	m, _ = send(m, mkKey("C"))
	_, cmd := send(m, mkKey("enter"))
	require.NotNil(t, cmd)
}

// TestAnAlreadyFinishedBuildIsAStatusLineNotAnError.
func TestAnAlreadyFinishedBuildIsAStatusLineNotAnError(t *testing.T) {
	m := cancelModel()
	m, _ = send(m, cancelledMsg{AlreadyFinished: true, Build: sampleBuild()})
	require.NoError(t, m.err)
	require.Contains(t, m.status, "already finished")
}

// TestCOnAFinishedBuildSaysSoWithoutAsking.
func TestCOnAFinishedBuildSaysSoWithoutAsking(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.builds.setItems([]provider.Build{sampleBuild()}) // failed, so finished
	m.focus = focusBuilds
	m, cmd := send(m, mkKey("C"))
	require.Equal(t, overlayNone, m.overlay)
	require.Nil(t, cmd)
	require.Contains(t, m.status, "already finished")
}

// TestCancellingLeavesTheWatchRunning: a cancelled build still transitions,
// and the watch is what reports the transition.
func TestCancellingLeavesTheWatchRunning(t *testing.T) {
	m := cancelModel()
	m.watchCancel = func() {}
	m, _ = send(m, cancelledMsg{Build: sampleBuild()})
	require.NotNil(t, m.watchCancel)
}

// TestLowercaseCDoesNotCancel.
func TestLowercaseCDoesNotCancel(t *testing.T) {
	m := cancelModel()
	m, cmd := send(m, mkKey("c"))
	require.Nil(t, cmd)
	require.Equal(t, overlayNone, m.overlay)
}

// TestConfirmKeysDoNotReachThePanels.
func TestConfirmKeysDoNotReachThePanels(t *testing.T) {
	m := cancelModel()
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "A"}, {Key: "B"}}})
	m, _ = send(m, mkKey("C"))
	before := m.plans.cursor
	m, _ = send(m, mkKey("j"))
	require.Equal(t, before, m.plans.cursor)
}

// TestAStaleFormErrorIsDropped: leaving a form and opening another must not
// let the first one's failure land on the second.
func TestAStaleFormErrorIsDropped(t *testing.T) {
	m := formModel()
	stale := m.formGen
	m.formGen++
	m, _ = send(m, errMsg{Err: errBoom, Where: "run", Stream: streamRun, Gen: stale})
	require.NoError(t, m.err)

	m, _ = send(m, errMsg{Err: errBoom, Where: "run", Stream: streamRun, Gen: m.formGen})
	require.Error(t, m.err)
}

// TestASynthesizedRequiredSecretIsMasked covers a required name the plan does
// not declare. It is added as an empty row, and it must still obey the name
// heuristic: without it db_password echoes what is typed.
func TestASynthesizedRequiredSecretIsMasked(t *testing.T) {
	ref := sampleRef()
	ref.Target.Required = []string{"db_password", "cluster_name"}

	fs := buildFields(sampleBase(), ref)
	var pw formField
	for _, f := range fs {
		if f.Name == "db_password" {
			pw = f
		}
	}
	require.Equal(t, "db_password", pw.Name, "the required name the plan does not declare is still a row")
	require.True(t, pw.Secret)

	m := formModel()
	m.form.ref = ref
	m.form.fields = fs
	for i := range m.form.fields {
		if m.form.fields[i].Name == "db_password" {
			m.form.fields[i].Value, m.form.fields[i].Touched = "hunter2", true
		}
	}
	require.NotContains(t, m.View(), "hunter2")
}

// TestTheFooterCountsWhatWouldBeSent, not what was touched. Typing a value
// back to the plan's own sends nothing, so it is not a change.
func TestTheFooterCountsWhatWouldBeSent(t *testing.T) {
	m := formModel()
	// cluster_type is the plan's k8s; typing it again changes nothing.
	for i := range m.form.fields {
		if m.form.fields[i].Name == "cluster_type" {
			m.form.fields[i].Value, m.form.fields[i].Touched = "k8s", true
		}
	}
	require.Contains(t, m.View(), "1 of 3 changed", "two fields are touched, one differs")
}

// TestTheFooterCountsAnUntouchedPresetDefault: a preset default that differs
// from the plan's value is sent even though nobody typed it, so the footer
// must count it.
func TestTheFooterCountsAnUntouchedPresetDefault(t *testing.T) {
	base := sampleBase()
	base.Vars[1].PlanValue = "dcos" // the preset's k8s now differs from the plan
	m := formModel()
	m.form.base = base
	m.form.fields = buildFields(base, sampleRef())
	// Fill the required field, or no set can be sent at all.
	m.form.fields[0].Value, m.form.fields[0].Touched = "beta", true

	require.Contains(t, m.View(), "2 of 3 changed",
		"the typed cluster_name and the untouched cluster_type both go to Bamboo")
}

// TestTheFooterFallsBackWhileAValueIsInvalid: nothing can be sent while a
// rule is broken, so the count reports what the user has touched instead.
func TestTheFooterFallsBackWhileAValueIsInvalid(t *testing.T) {
	m := formModel() // cluster_name is required and formModel types it
	require.Contains(t, m.View(), "1 of 3 changed")

	m.form.fields[0].Value = "" // required, now empty: ValidateVars refuses
	require.Contains(t, m.View(), "1 of 3 changed")
}

// TestSpaceCyclesAnOptionsField is the second control the actions design
// documents beside enter.
func TestSpaceCyclesAnOptionsField(t *testing.T) {
	m := formModel()
	m.form.cursor = 1
	m, _ = send(m, mkKey(" "))
	require.False(t, m.form.editing)
	require.Equal(t, "dcos", m.form.fields[1].Value)
	require.True(t, m.form.fields[1].Touched)

	m, _ = send(m, mkKey(" "))
	require.Equal(t, "k8s", m.form.fields[1].Value, "cycling wraps")
}

// TestSpaceOnAFreeTextFieldDoesNotOpenAnEditor: space is a cycle key only,
// so it cannot become a way to start typing a space into a value.
func TestSpaceOnAFreeTextFieldDoesNotOpenAnEditor(t *testing.T) {
	m := formModel()
	m.form.cursor = 0 // cluster_name: free text
	m, _ = send(m, mkKey(" "))
	require.False(t, m.form.editing)
	require.Equal(t, "beta", m.form.fields[0].Value)
}

// TestAZeroVariablePlanCanStillBeRun: an empty VarSet is what bam run sends
// for a plan that declares nothing, so the form must not swallow ctrl-R.
func TestAZeroVariablePlanCanStillBeRun(t *testing.T) {
	m := formModel()
	m.form.base = app.VarSet{DeclaredKnown: true, Declared: map[string]bool{}}
	m.form.fields = nil
	m.form.ref.Target = nil

	m2, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.Equal(t, screenForm, m2.screen, "the form stays open until the build comes back")
	require.NotNil(t, cmd, "ctrl-R sent the run")

	m3, _ := send(m, mkKey("d"))
	require.Equal(t, overlayDryRun, m3.overlay)
	require.Contains(t, m3.View(), "no variables would be sent")
}

// TestCIsIgnoredOffThePlanScreen: currentBuild falls back to the Builds
// cursor, so without a screen guard a C struck on Home offers to stop a build
// from a table the user is not operating on.
func TestCIsIgnoredOffThePlanScreen(t *testing.T) {
	for _, f := range []focus{focusPlans, focusPresets} {
		m := cancelModel()
		m.screen, m.focus = screenHome, f
		m, cmd := send(m, mkKey("C"))
		require.Equal(t, overlayNone, m.overlay, "focus %v must not open the confirmation", f)
		require.Nil(t, cmd)
	}
}

// TestCStillWorksFromEveryPlanScreenPanel is the other half: C acts on the
// open build from Builds, Stages and Tree.
func TestCStillWorksFromEveryPlanScreenPanel(t *testing.T) {
	for _, f := range []focus{focusBuilds, focusStages, focusTree} {
		m := cancelModel()
		m.focus = f
		if f != focusBuilds {
			b := m.builds.items[0]
			m.detail = &b
		}
		m, _ = send(m, mkKey("C"))
		require.Equal(t, overlayConfirm, m.overlay, "focus %v must still ask", f)
	}
}

// TestAStaleCancelResultIsDropped: a cancel confirmed just before the user
// left the build must not report "cancelled OLD" over the build they are
// looking at now.
func TestAStaleCancelResultIsDropped(t *testing.T) {
	m := cancelModel()
	m, _ = send(m, mkKey("C"))
	m, cmd := send(m, mkKey("y"))
	require.NotNil(t, cmd)
	sent := cmd().(cancelledMsg)
	require.Equal(t, m.cancelGen, sent.Gen)

	// The user leaves the build before the reply lands.
	m.leaveBuild()
	m, _ = send(m, sent)
	require.Empty(t, m.status, "the reply belongs to a build that is no longer on screen")

	// The same reply on the screen that asked for it is still accepted.
	m2 := cancelModel()
	m2, _ = send(m2, mkKey("C"))
	m2, cmd2 := send(m2, mkKey("y"))
	m2, _ = send(m2, cmd2().(cancelledMsg))
	require.Contains(t, m2.status, "cancelled PROJ-BUILD-44")
}

// TestAStaleCancelErrorIsDropped is the same rule for the failure path.
func TestAStaleCancelErrorIsDropped(t *testing.T) {
	m := cancelModel()
	m, _ = send(m, mkKey("C"))
	m, _ = send(m, mkKey("y"))
	stale := errMsg{Err: errs.Bamboof("boom"), Where: "cancel", Stream: streamCancel, Gen: m.cancelGen}

	m.leaveBuild()
	m, _ = send(m, stale)
	require.NoError(t, m.err, "a failure for an abandoned cancel is not the current screen's")
}

// TestEveryFormCommandTagsItsError: the model drops a stale error by Stream
// and Gen, so a command that returns an untagged one is accepted whatever
// screen the user has moved to. These are the three that can fail.
func TestEveryFormCommandTagsItsError(t *testing.T) {
	const gen = 7
	svc := testService()
	f := svc.P.(*fake.Provider)

	t.Run("resolving the plan", func(t *testing.T) {
		requireTaggedRunError(t, openFormCmd(t.Context(), svc, "no such plan", nil, "", gen), gen)
	})

	t.Run("fetching the variables", func(t *testing.T) {
		f.VariablesErr = errBoom
		defer func() { f.VariablesErr = nil }()
		requireTaggedRunError(t, openFormCmd(t.Context(), svc, "PROJ-PROV", nil, "", gen), gen)
	})

	t.Run("triggering", func(t *testing.T) {
		f.TriggerErr = errBoom
		defer func() { f.TriggerErr = nil }()
		ref := app.PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV"}
		requireTaggedRunError(t, runCmd(t.Context(), svc, ref, app.VarSet{}, gen), gen)
	})
}

func requireTaggedRunError(t *testing.T, cmd tea.Cmd, gen int) {
	t.Helper()
	msg, ok := cmd().(errMsg)
	require.True(t, ok, "the command should have failed")
	require.Equal(t, streamRun, msg.Stream, "an untagged error is never dropped as stale")
	require.Equal(t, gen, msg.Gen)
}

// TestTheFormSendsExactlyWhatBamRunSends is the invariant behind the
// untouched-secret rule: with nothing typed, the form's set and
// Service.ResolveVars' set are the same, secrets included. A form that
// dropped an untouched preset secret would run the build with the plan's
// credentials instead of the preset's.
func TestTheFormSendsExactlyWhatBamRunSends(t *testing.T) {
	svc := testService()
	svc.Getenv = func(k string) string {
		if k == "LAB_TOKEN" {
			return "shhh"
		}
		return ""
	}
	ref := app.PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV", Target: &config.ResolvedTarget{
		Name: "provision-lab",
		Target: config.Target{Plan: "PROJ-PROV", Defaults: config.StringMap{
			"db_password": "hunter2",      // a secret by name
			"token":       "${LAB_TOKEN}", // a secret by env reference
			"region":      "eu-west",      // not a secret at all
		}},
	}}

	// What bam run provision-lab would send.
	want, err := svc.ResolveVars(t.Context(), ref, app.VarOptions{})
	require.NoError(t, err)

	// What the form sends with nothing typed.
	base, err := svc.VarBase(t.Context(), ref, "")
	require.NoError(t, err)
	f := formState{ref: ref, base: base, fields: buildFields(base, ref)}
	got, err := app.ValidateVars(ref, base, f.flags(), svc.Getenv)
	require.NoError(t, err)

	require.Equal(t, want.Changed(), f.stripUntypedMasks(got).Changed())
	require.Equal(t, "shhh", want.Changed()["token"], "the resolved secret is what goes to Bamboo")
	require.Equal(t, map[string]bool{"db_password": true, "token": true}, got.Secret())
}

func TestFormShowsTheRevisionRowLast(t *testing.T) {
	v := formModel().View()
	require.Contains(t, v, "revision")
	require.Contains(t, v, "newest")
	require.Contains(t, v, "1 of 3 changed", "the revision row is not a variable")
}

func TestFormCursorReachesTheRevisionRowAndWraps(t *testing.T) {
	m := formModel()
	for i := 0; i < 3; i++ {
		m, _ = send(m, mkKey("j"))
	}
	require.True(t, m.form.onRevision())
	m, _ = send(m, mkKey("j"))
	require.Equal(t, 0, m.form.cursor, "down from the revision row wraps to the first variable")
}

func TestFormTypesARevision(t *testing.T) {
	m := formModel()
	m.form.cursor = len(m.form.fields)
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.form.editing)
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc1234")})
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, "abc1234", m.form.revision)
	require.Equal(t, 1, m.changedCount(), "typing a revision changes no variable")
	require.Contains(t, m.View(), "abc1234")
}

func TestCtrlRSendsTheRevision(t *testing.T) {
	m := formModel()
	m.form.revision = "abc1234"
	m.revalidate()
	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.NotNil(t, cmd)
	cmd()
	f := m.svc.P.(*fake.Provider)
	require.Len(t, f.Triggered, 1)
	require.Equal(t, "abc1234", f.Triggered[0].Revision)
}

func TestDryRunShowsTheRevision(t *testing.T) {
	m := formModel()
	m.form.revision = "abc1234"
	require.Contains(t, m.dryRunBody(60), "revision abc1234")
}

func TestTheRevisionDoesNotSurviveTheForm(t *testing.T) {
	m := formModel()
	m.form.revision = "abc1234"
	m, _ = send(m, mkKey("esc"))
	require.NotEqual(t, screenForm, m.screen)
	require.Equal(t, "", m.form.revision)

	m.screen, m.form.loading = screenForm, true
	m, _ = send(m, formLoadedMsg{Gen: m.formGen, Ref: sampleRef(), Target: "provision-lab", Base: sampleBase()})
	require.Equal(t, "", m.form.revision)
}

func TestAPlanWithNoVariablesCanStillTakeARevision(t *testing.T) {
	m := formModel()
	m.form.fields = nil
	m.form.cursor = 0
	require.True(t, m.form.onRevision())
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.form.editing)
}

// TestAPlanWithNoVariablesSendsATypedRevision: the only row is Revision, and
// ctrl-R after typing on it must reach the trigger with that value.
func TestAPlanWithNoVariablesSendsATypedRevision(t *testing.T) {
	m := formModel()
	m.form.base = app.VarSet{DeclaredKnown: true, Declared: map[string]bool{}}
	m.form.fields = nil
	m.form.ref.Target = nil
	m.form.cursor = 0
	require.True(t, m.form.onRevision())

	m, _ = send(m, mkKey("enter"))
	require.True(t, m.form.editing)
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc1234")})
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, "abc1234", m.form.revision)

	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.NotNil(t, cmd)
	cmd()
	f := m.svc.P.(*fake.Provider)
	require.Len(t, f.Triggered, 1)
	require.Equal(t, "abc1234", f.Triggered[0].Revision)
}

// TestAMaskedReadbackIsNeverSentBack is the one value the form does strip.
func TestAMaskedReadbackIsNeverSentBack(t *testing.T) {
	base := app.VarSet{
		DeclaredKnown: true,
		Declared:      map[string]bool{"ssh_key": true},
		Vars: []app.ResolvedVar{
			{Name: "ssh_key", Value: app.MaskedDisplay, PlanValue: "real-key",
				Source: "plan", Declared: true, Secret: true},
		},
	}
	ref := app.PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV"}
	f := formState{ref: ref, base: base, fields: buildFields(base, ref)}

	vs, err := app.ValidateVars(ref, base, f.flags(), func(string) string { return "" })
	require.NoError(t, err)
	require.NotContains(t, f.stripUntypedMasks(vs).Changed(), "ssh_key",
		"******** must never overwrite the real secret")
}

const hintCommit = "9b30b9aba03a4f45881629b03531006b89c1e0e7"

func hintedForm() Model {
	m := formModel()
	m.form.fromBuild, m.form.fromRevision = "PROJ-PROV12-41", hintCommit
	return m
}

func TestFormOffersTheBuildsRevision(t *testing.T) {
	m := hintedForm()
	v := m.View()
	require.Contains(t, v, "newest")
	require.Contains(t, v, "#41 built 9b30b9a")
	require.Equal(t, "", m.form.revision, "offered, not filled")
}

func TestFormWithoutAHintSaysCommitToBuild(t *testing.T) {
	require.Contains(t, formModel().View(), "commit to build")
}

func TestTheFKeyFillsTheRevisionOnlyOnTheRevisionRow(t *testing.T) {
	m := hintedForm()
	m, _ = send(m, mkKey("f"))
	require.Equal(t, "", m.form.revision, "f on a variable does nothing")

	m.form.cursor = len(m.form.fields)
	m, _ = send(m, mkKey("f"))
	require.Equal(t, hintCommit, m.form.revision)
	require.Contains(t, m.View(), "from #41")

	m = formModel()
	m.form.cursor = len(m.form.fields)
	m, _ = send(m, mkKey("f"))
	require.Equal(t, "", m.form.revision, "no hint, nothing to use")
}

func TestTheKeyLineOffersFOnlyOnTheRevisionRowWithAHint(t *testing.T) {
	m := hintedForm()
	require.NotContains(t, m.keyLine(), "f use")
	m.form.cursor = len(m.form.fields)
	require.Contains(t, m.keyLine(), "f use #41's revision")
	m, _ = send(m, mkKey("f"))
	require.NotContains(t, m.keyLine(), "f use", "the commit is already taken")
	m = formModel()
	m.form.cursor = len(m.form.fields)
	require.NotContains(t, m.keyLine(), "f use")
}

func TestCtrlRSendsAReusedRevisionWithItsBuild(t *testing.T) {
	m := hintedForm()
	m.form.cursor = len(m.form.fields)
	m, _ = send(m, mkKey("f"))
	m.revalidate()
	want := sampleRef()
	want.Revision, want.RevisionFrom = hintCommit, "PROJ-PROV12-41"
	require.Equal(t, want, m.form.planRef())
	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.NotNil(t, cmd)
	cmd()
	f := m.svc.P.(*fake.Provider)
	require.Len(t, f.Triggered, 1)
	require.Equal(t, hintCommit, f.Triggered[0].Revision)
}

// Typing over a reused revision makes it a typed one: no from #N label.
func TestEditingAReusedRevisionDropsItsBuild(t *testing.T) {
	m := hintedForm()
	m.form.cursor = len(m.form.fields)
	m, _ = send(m, mkKey("f"))
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, hintCommit[:len(hintCommit)-1], m.form.revision)
	require.Equal(t, "", m.form.planRef().RevisionFrom)
}

func TestOpenFormCmdReadsTheBuildsRevision(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.History = map[string][]provider.Build{"PROJ-PROV": {{Key: "PROJ-PROV-41", Number: 41, Reason: "Manual run by jdoe", Revision: hintCommit}}}
	f.BuildVars = map[string]map[string]string{"PROJ-PROV-41": {}}
	msg := openFormCmd(t.Context(), svc, "PROJ-PROV", nil, "PROJ-PROV-41", 1)()
	loaded, ok := msg.(formLoadedMsg)
	require.True(t, ok, "%#v", msg)
	require.Equal(t, "PROJ-PROV-41", loaded.FromBuild)
	require.Equal(t, hintCommit, loaded.FromRevision)
}

// The hint is a convenience: a build with no revision (or a failed read)
// opens the form normally, without a hint and without an error.
func TestOpenFormCmdWithoutARevisionHasNoHint(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.History = map[string][]provider.Build{"PROJ-PROV": {{Key: "PROJ-PROV-41", Number: 41, Reason: "Manual run by jdoe"}}}
	f.BuildVars = map[string]map[string]string{"PROJ-PROV-41": {}}
	msg := openFormCmd(t.Context(), svc, "PROJ-PROV", nil, "PROJ-PROV-41", 1)()
	loaded, ok := msg.(formLoadedMsg)
	require.True(t, ok, "%#v", msg)
	require.Equal(t, "", loaded.FromRevision)
}

func TestFormLoadedCarriesTheHintAndReopeningClearsIt(t *testing.T) {
	m := formModel()
	m.form.loading = true
	m, _ = send(m, formLoadedMsg{Gen: m.formGen, Ref: sampleRef(), Target: "provision-lab", Base: sampleBase(),
		FromBuild: "PROJ-PROV12-41", FromRevision: hintCommit})
	require.Equal(t, hintCommit, m.form.fromRevision)
	require.Equal(t, "", m.form.revision)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, "", m.form.fromRevision)
}

// At 80 columns the status bar must still fit the key line with the f hint;
// a line too long for the bar loses its last hints, such as ^R run.
func TestTheFHintFitsTheStatusBarAt80Columns(t *testing.T) {
	m := hintedForm()
	m.form.cursor = len(m.form.fields)
	require.Contains(t, m.statusBar(80), "f use #41's revision")
	require.Contains(t, m.statusBar(80), "^R run")
}

// A failed read of the build's revision (here: the build is gone) is not an
// error: the form opens on the build's variables, without a hint.
func TestOpenFormCmdWithAFailedRevisionReadHasNoHint(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.History = map[string][]provider.Build{}
	f.BuildVars = map[string]map[string]string{"PROJ-PROV-41": {}}
	msg := openFormCmd(t.Context(), svc, "PROJ-PROV", nil, "PROJ-PROV-41", 1)()
	loaded, ok := msg.(formLoadedMsg)
	require.True(t, ok, "%#v", msg)
	require.Equal(t, "PROJ-PROV-41", loaded.FromBuild)
	require.Equal(t, "", loaded.FromRevision)
}

// typeIn sends each rune of s as a key.
func typeIn(m Model, s string) Model {
	for _, r := range s {
		m, _ = send(m, mkKey(string(r)))
	}
	return m
}

// TestAAddsAnUndeclaredVariableThatIsSent: the form's --var for a name the
// plan does not declare. It warns, as bam run does, and is sent on ctrl-R.
func TestAAddsAnUndeclaredVariableThatIsSent(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("a"))
	require.True(t, m.form.naming)
	m = typeIn(m, "global_flag")
	require.Contains(t, m.View(), "global_flag")
	m, _ = send(m, mkKey("enter"))
	require.False(t, m.form.naming)
	require.True(t, m.form.editing, "the value is typed next")
	require.Equal(t, 3, m.form.cursor, "the row goes after the declared ones")

	m = typeIn(m, "on")
	m, _ = send(m, mkKey("enter"))
	added := m.form.fields[3]
	require.Equal(t, "global_flag", added.Name)
	require.Equal(t, "on", added.Value)
	require.True(t, added.Added)
	require.Contains(t, m.View(), "not declared")
	require.Contains(t, m.dryRunBody(76), "global_flag=on")

	// Tab and a dry run keep it.
	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("d"))
	m, _ = send(m, mkKey("esc"))
	require.Len(t, m.form.fields, 4)

	m.revalidate()
	require.True(t, m.canRun(), "an undeclared name warns, it does not block")
	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.NotNil(t, cmd)
	cmd()
	f := m.svc.P.(*fake.Provider)
	require.Equal(t, map[string]string{"cluster_name": "beta", "global_flag": "on"}, f.Triggered[0].Variables)
}

// TestAddingADeclaredNameEditsThatVariable instead of adding a duplicate.
func TestAddingADeclaredNameEditsThatVariable(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("a"))
	m = typeIn(m, "cluster_name")
	m, _ = send(m, mkKey("enter"))
	require.Len(t, m.form.fields, 3)
	require.Equal(t, 0, m.form.cursor)
	require.True(t, m.form.editing)

	// An options field is moved to, not opened as free text.
	m, _ = send(m, mkKey("esc"))
	m, _ = send(m, mkKey("a"))
	m = typeIn(m, "cluster_type")
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, 1, m.form.cursor)
	require.False(t, m.form.editing)
}

// TestAnAddedSecretNameIsMaskedEverywhere.
func TestAnAddedSecretNameIsMaskedEverywhere(t *testing.T) {
	const sentinel = "correct-horse-battery-staple"
	m := formModel()
	m, _ = send(m, mkKey("a"))
	m = typeIn(m, "db_password")
	m, _ = send(m, mkKey("enter"))
	m = typeIn(m, sentinel)
	require.NotContains(t, m.View(), sentinel)
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.form.fields[3].Secret)
	require.NotContains(t, m.View(), sentinel)
	require.Contains(t, m.dryRunBody(76), "db_password="+app.MaskedDisplay)
	require.NotContains(t, m.dryRunBody(76), sentinel)
}

// TestXRemovesOnlyAnAddedRow: a declared variable cannot be removed.
func TestXRemovesOnlyAnAddedRow(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("x"))
	require.Len(t, m.form.fields, 3, "a declared row stays")

	m, _ = send(m, mkKey("a"))
	m = typeIn(m, "extra")
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, mkKey("esc"))
	require.Len(t, m.form.fields, 4)
	m, _ = send(m, mkKey("x"))
	require.Len(t, m.form.fields, 3)
	require.True(t, m.form.onRevision(), "the cursor falls to the next row")
}

// TestAnEmptyOrBadNameAddsNothing.
func TestAnEmptyOrBadNameAddsNothing(t *testing.T) {
	m := formModel()
	m, _ = send(m, mkKey("a"))
	m, _ = send(m, mkKey("enter"))
	require.False(t, m.form.editing)
	require.Len(t, m.form.fields, 3)

	m, _ = send(m, mkKey("a"))
	m = typeIn(m, "a=b")
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.form.naming, "the name stays open to fix")
	require.Error(t, m.form.err)
	require.Len(t, m.form.fields, 3)

	m, _ = send(m, mkKey("esc"))
	require.False(t, m.form.editing)
	require.Len(t, m.form.fields, 3)
	require.NoError(t, m.form.err, "the name it was about is gone")
	require.NotContains(t, m.View(), "is not a variable name")
}

// TestAFailedFormLoadStopsSayingLoading: when the plan or its variables
// cannot be read, the form must not keep promising them.
func TestAFailedFormLoadStopsSayingLoading(t *testing.T) {
	m := formModel()
	m.form = formState{loading: true}
	require.Contains(t, m.View(), "loading the plan's variables")

	m, _ = send(m, errMsg{Err: errBoom, Where: "run", Stream: streamRun, Gen: m.formGen})
	v := m.View()
	require.NotContains(t, v, "loading the plan's variables")
	require.Contains(t, v, "could not load the plan's variables")
	require.NotContains(t, v, "^R run", "no key but esc works on a form that never loaded")

	m, _ = send(m, mkKey("enter"))
	require.False(t, m.form.editing, "a failed form still has nothing to edit")
}

// TestALongFormErrorWraps: a Bamboo refusal is often wider than the form, and
// its reason is at the end.
func TestALongFormErrorWraps(t *testing.T) {
	m := formModel()
	m.form.err = fmt.Errorf("Bamboo refused the trigger for PROJ-API: the plan is disabled by an administrator")
	body := m.formBody(40, 40)
	for _, l := range strings.Split(body, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), 40, l)
	}
	require.Contains(t, strings.Join(strings.Fields(ansi.Strip(body)), " "), "disabled by an administrator")
}
