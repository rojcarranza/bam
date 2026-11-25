package app

import (
	"slices"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func savedPreset() config.ResolvedTarget {
	return config.ResolvedTarget{Name: "deploy-qa", Target: config.Target{
		Plan: "PROJ-DEPLOY", Branch: "release-1",
		Defaults:     config.StringMap{"ENV": "qa", "db_password": "${DB_PASSWORD}"},
		Options:      config.StringListMap{"ENV": {"qa", "prod"}},
		Required:     []string{"ENV"},
		Timeout:      config.Duration(45 * time.Minute),
		Repositories: config.StringMap{"api": "main"},
	}}
}

var planVars = map[string]string{"ENV": "dev", "REGION": "eu", "api_secret": ""}

func TestPresetEditFormListsSavedThenOmittedVariables(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), planVars)
	var names []string
	for _, r := range f.Rows {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"ENV", "db_password", "REGION", "api_secret"}, names)
	assert.True(t, f.Rows[0].InPreset)
	assert.Equal(t, []string{"qa", "prod"}, f.Rows[0].Choices, "the preset's options are the choices")
	assert.False(t, f.Rows[2].InPreset)
	assert.Equal(t, "eu", f.Rows[2].Value, "an omitted variable shows the plan's value")
	assert.True(t, f.Rows[1].Secret)
	assert.True(t, f.Rows[3].Secret, "a secret by name is a secret even when the preset leaves it out")
	var keys []string
	for _, fl := range f.Fields {
		keys = append(keys, fl.Key+"="+fl.Value)
	}
	assert.Equal(t, []string{"name=deploy-qa", "branch=release-1", "repositories.api=main", "required=ENV", "timeout=45m"}, keys)
}

func TestPresetEditFormUntouchedChangesNothing(t *testing.T) {
	e := NewPresetEditForm(savedPreset(), planVars).Edit()
	assert.Equal(t, config.PresetEdit{}, e)
}

func TestPresetEditFormEditsEverything(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), planVars)
	f.Rows[0].Value = "prod"          // saved variable
	f.Rows[1].Removed = true          // saved secret removed
	f.Rows[2].Value = "us"            // omitted variable changed
	f.Rows[3].Value = "${API_SECRET}" // omitted secret given a reference
	f.Rows[0].Options = []string{"qa", "prod", "dev"}
	set := func(key, v string) {
		for i := range f.Fields {
			if f.Fields[i].Key == key {
				f.Fields[i].Value = v
			}
		}
	}
	set("name", "deploy-prod")
	set("branch", "release-2")
	set("repositories.api", "release-2")
	set("required", "ENV, REGION, ENV")
	set("timeout", "1h")
	e := f.Edit()
	assert.Equal(t, []config.VarValue{{Name: "ENV", Value: "prod"}, {Name: "REGION", Value: "us"}, {Name: "api_secret", Value: "${API_SECRET}"}}, e.Set)
	assert.Equal(t, []string{"db_password"}, e.Unset)
	assert.Equal(t, map[string][]string{"ENV": {"qa", "prod", "dev"}}, e.Options)
	assert.Equal(t, "deploy-prod", e.Rename)
	assert.Equal(t, "release-2", *e.Branch)
	assert.Equal(t, []config.RepoBranch{{Name: "api", Branch: "release-2"}}, *e.Repositories)
	assert.Equal(t, []string{"ENV", "REGION"}, *e.Required)
	assert.Equal(t, "1h", *e.Timeout)
}

func TestPresetEditFormOmittedVariableLeftAloneIsNotWritten(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), planVars)
	f.Rows[2].Removed = true // removing what the preset never set is nothing
	assert.Empty(t, f.Edit().Unset)
	assert.Empty(t, f.Edit().Set)
}

func TestPresetEditFormValidate(t *testing.T) {
	cases := map[string]func(*PresetForm){
		"name":       func(f *PresetForm) { f.Fields[0].Value = "Bad Name" },
		"timeout":    func(f *PresetForm) { f.Fields[4].Value = "soon" },
		"branch":     func(f *PresetForm) { f.Fields[1].Value = "" },
		"repository": func(f *PresetForm) { f.Fields[2].Value = "" },
		"options":    func(f *PresetForm) { f.Rows[0].Value = "dev" },
	}
	for name, mutate := range cases {
		f := NewPresetEditForm(savedPreset(), planVars)
		require.NoError(t, f.Validate(), name)
		mutate(&f)
		assert.Error(t, f.Validate(), name)
	}
}

func TestDurationTextIsHowPeopleWriteIt(t *testing.T) {
	for d, want := range map[time.Duration]string{45 * time.Minute: "45m", time.Hour: "1h", 90 * time.Minute: "1h30m", 30 * time.Second: "30s", 0: ""} {
		assert.Equal(t, want, durationText(config.Duration(d)), d.String())
	}
}

func TestPresetEditFormRetypingTheSameTimeoutIsNoChange(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), planVars)
	f.Fields[4].Value = "45m0s"
	assert.Nil(t, f.Edit().Timeout)
	f.Fields[4].Value = "0.75h"
	assert.Nil(t, f.Edit().Timeout, "the same duration written another way")
	f.Fields[4].Value = "50m"
	assert.Equal(t, "50m", *f.Edit().Timeout)
}

func TestPresetEditFormRetypingTheSameRequiredListIsNoChange(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), planVars)
	at := slices.IndexFunc(f.Fields, func(fl FormField) bool { return fl.Key == "required" })
	require.GreaterOrEqual(t, at, 0)
	f.Fields[at].Value = " ENV , ENV,"
	assert.Nil(t, f.Edit().Required, "the same list written another way")
	f.Fields[at].Value = "ENV, REGION"
	assert.Equal(t, []string{"ENV", "REGION"}, *f.Edit().Required)
}

// A hand-written literal secret is never drawn: the row says so and the
// form leaves its value alone unless a variable name replaces it.
func TestPresetEditFormMarksLiteralSecrets(t *testing.T) {
	p := savedPreset()
	p.Defaults["api_secret"] = "hunter2"
	f := NewPresetEditForm(p, nil)
	var lit, ref *FormRow
	for i := range f.Rows {
		switch f.Rows[i].Name {
		case "api_secret":
			lit = &f.Rows[i]
		case "db_password":
			ref = &f.Rows[i]
		}
	}
	require.NotNil(t, lit)
	assert.True(t, lit.Secret)
	assert.True(t, lit.Literal)
	assert.False(t, ref.Literal)
	assert.Empty(t, f.Edit().Set)
}

func TestPresetEditFormRefusesNamesBamKnowsAreWrong(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), planVars)
	f.TakenNames = []string{"deploy-qa", "other"}
	f.Fields[0].Value = "other"
	assert.ErrorContains(t, f.Validate(), "already exists")
	f.Fields[0].Value = "deploy-eu"
	assert.NoError(t, f.Validate())
	f.RenameBlocked = `preset "deploy-qa" is used by set "train"`
	assert.ErrorContains(t, f.Validate(), "used by set")
	f.Fields[0].Value = "deploy-qa"
	assert.NoError(t, f.Validate(), "not renamed, so not blocked")
}

func TestPresetEditFormRefusesWhatAnotherFileHolds(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), planVars)
	f.Path = "top.yaml"
	f.Held = Held{Only: map[string]string{"variables.ENV": "low.yaml", "branch": "low.yaml"}, Also: map[string]string{}}
	require.NoError(t, f.Validate())
	f.Rows[0].Removed = true
	assert.ErrorContains(t, f.Validate(), "low.yaml")
	f.Rows[0].Removed = false
	f.Fields[1].Value = ""
	assert.ErrorContains(t, f.Validate(), "low.yaml")
}

func TestPresetEditFormRemovingWhatBothHoldIsAllowed(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), planVars)
	f.Held = Held{Only: map[string]string{}, Also: map[string]string{"variables.ENV": "low.yaml"}}
	f.Rows[0].Removed = true
	assert.NoError(t, f.Validate())
}

// A shared preset cannot take a name another repository's own preset has, and
// the form says which repository.
func TestPresetEditFormRefusesANameAnotherRepositoryHas(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), planVars)
	f.TakenElsewhere = map[string]string{"lab-two": "/somewhere/else"}
	f.Fields[0].Value = "lab-two"
	err := f.Validate()
	assert.ErrorContains(t, err, "already exists in the repository /somewhere/else")
	f.Fields[0].Value = "lab-three"
	assert.NoError(t, f.Validate())
}

// Emptying a plan variable the preset leaves out writes an empty value, as
// --var NAME= does; a row left alone, or removed with x, writes nothing.
func TestPresetEditFormWritesAnEmptiedPlanVariable(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), planVars)
	for i := range f.Rows {
		if f.Rows[i].Name == "REGION" {
			require.False(t, f.Rows[i].InPreset)
			f.Rows[i].Value = ""
		}
	}
	assert.Equal(t, []config.VarValue{{Name: "REGION", Value: ""}}, f.Edit().Set)
	for i := range f.Rows {
		if f.Rows[i].Name == "REGION" {
			f.Rows[i].Value = f.Rows[i].Orig
		}
	}
	assert.Empty(t, f.Edit().Set)
}
