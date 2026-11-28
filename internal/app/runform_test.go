package app

import (
	"testing"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/stretchr/testify/assert"
)

// Intent: the shared line names the plan, carries branch, revision and
// repositories, lists only values that differ from the plan, quotes what the
// shell would split, and writes a secret as a shell reference.
func TestShareCommand(t *testing.T) {
	ref := PlanRef{PlanKey: "PROJ-DEPLOY12", MasterKey: "PROJ-DEPLOY", Branch: "feature/x y", Revision: "abc123",
		Repositories: map[string]string{"repo-b": "main", "repo-a": "feature/PROJ-1"}}
	f := PresetForm{Plan: "PROJ-DEPLOY", RunMode: true, Rows: []FormRow{
		{Name: "region", Value: "eu", Plan: "eu"},
		{Name: "target_env", Value: "qa env", Plan: "staging"},
		{Name: "extra", Value: "", Plan: "", Undeclared: true},
		{Name: "stored_token", Value: "${STORED_TOKEN}", Secret: true, CanStore: true, Commented: true},
		{Name: "deploy_token", Value: "${DEPLOY_TOKEN}", Secret: true},
		{Name: "typed_ref", Value: "${TYPED}", Plan: "x"},
		{Name: "stored_ref", Value: "${LITERAL}", Plan: "${LITERAL}", Orig: "${LITERAL}"},
	}}
	assert.Equal(t,
		`bam run PROJ-DEPLOY --branch 'feature/x y' --revision abc123 --repository repo-a=feature/PROJ-1 --repository repo-b=main `+
			`--var 'target_env=qa env' --var extra= --var deploy_token="${DEPLOY_TOKEN:?}" --var typed_ref="${TYPED:?}"`,
		ShareCommand(ref, f))
}

// Intent: a plan that no value changes shares as just its key.
func TestShareCommandOfAnUnchangedPlan(t *testing.T) {
	f := PresetForm{Rows: []FormRow{{Name: "region", Value: "eu", Plan: "eu"}}}
	assert.Equal(t, "bam run PROJ-DEPLOY", ShareCommand(PlanRef{MasterKey: "PROJ-DEPLOY"}, f))
}

// Intent: a secret typed with --var opens as a shell reference marked literal,
// and stays out of the edits so the run still sends what was typed.
func TestNewRunFormKeepsATypedSecretLiteral(t *testing.T) {
	ref := PlanRef{MasterKey: "PROJ-DEPLOY"}
	vs := VarSet{Vars: []ResolvedVar{{Name: "db_password", Value: "x", Source: "flag", Secret: true, Declared: true}}}
	f := NewRunForm(ref, vs)
	assert.Equal(t, "${DB_PASSWORD}", f.Rows[0].Value)
	assert.True(t, f.Rows[0].Literal)
	assert.Empty(t, f.RunEdits())
	assert.NotContains(t, ShareCommand(ref, f), "x ")
}

// Intent: the printed line names the plan, so a preset's rules and unset
// environment variables never stop ctrl-s.
func TestApplyEditedIgnoresPresetRules(t *testing.T) {
	tgt := &config.ResolvedTarget{}
	tgt.Name, tgt.Options = "p", map[string][]string{"env": {"qa"}}
	ref := PlanRef{MasterKey: "PROJ-DEPLOY", Target: tgt}
	base := VarSet{Declared: map[string]bool{"env": true}, DeclaredKnown: true,
		Vars: []ResolvedVar{{Name: "env", Value: "qa", PlanValue: "qa", Declared: true}}}
	_, err := ApplyEdited(ref, base, nil, []Edit{{Name: "env", Value: "${UNSET_ENV}"}})
	assert.NoError(t, err)
}
