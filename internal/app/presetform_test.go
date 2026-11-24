package app

import (
	"testing"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/stretchr/testify/assert"
)

func formDraft() config.TargetDraft {
	return config.TargetDraft{Name: "deploy-qa", Plan: "PROJ-DEPLOY", Vars: []config.DraftVar{
		{Name: "target_env", Value: "staging"},
		{Name: "version", Value: ""},
		{Name: "deploy_token", Value: "${DEPLOY_TOKEN}", Commented: true},
		{Name: "api_key", Value: "${API_KEY}"},
	}}
}

// Choices start with the value the draft holds, then the plan default, then
// recent values; none repeats, and a secret never gets any.
func TestFormChoicesOrderAndDedup(t *testing.T) {
	f := NewPresetForm(formDraft(), map[string]string{"target_env": "qa", "version": ""})
	f.AddRecent([]RecentVar{{Name: "target_env", Values: []string{"staging", "dev", "prod"}}})
	assert.Equal(t, []string{"staging", "qa", "dev", "prod"}, f.Rows[0].Choices)
	assert.Equal(t, []string{""}, f.Rows[1].Choices)
	assert.Empty(t, f.Rows[2].Choices)
	assert.True(t, f.Rows[2].Secret)
	assert.True(t, f.Rows[2].CanStore)
	assert.False(t, f.Rows[3].CanStore, "a secret the plan does not store has only the ${ENV} line")
}

// Applying keeps every declared variable, writes chosen values, and a secret
// switched off the stored value becomes an active ${ENV} line.
func TestFormApply(t *testing.T) {
	d := formDraft()
	f := NewPresetForm(d, map[string]string{"target_env": "qa"})
	assert.Equal(t, 1, countDiffer(f), "staging differs from qa; version has no default and no value")
	f.Rows[0].Value = "qa"
	f.Rows[1].Value = "1.2"
	f.Rows[2].Commented = false
	f.Rows[2].Value = "${QA_TOKEN}"
	got := f.Apply(d)
	assert.Equal(t, []config.DraftVar{
		{Name: "target_env", Value: "qa"},
		{Name: "version", Value: "1.2"},
		{Name: "deploy_token", Value: "${QA_TOKEN}"},
		{Name: "api_key", Value: "${API_KEY}"},
	}, got.Vars)
	assert.True(t, d.Vars[2].Commented, "the draft passed in is not changed")
	assert.Equal(t, 2, countDiffer(f))
}

func countDiffer(f PresetForm) int {
	n := 0
	for _, r := range f.Rows {
		if r.Differs() {
			n++
		}
	}
	return n
}

func TestValidEnvName(t *testing.T) {
	assert.True(t, ValidEnvName("QA_TOKEN"))
	assert.False(t, ValidEnvName("1X"))
	assert.False(t, ValidEnvName("A B"))
	assert.False(t, ValidEnvName(""))
}
