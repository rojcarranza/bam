package app

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withEnv(s *Service, env map[string]string) { s.Getenv = func(k string) string { return env[k] } }

func TestResolveVarsPrecedenceAndMasking(t *testing.T) {
	s := newService(t, fakeBamboo())
	withEnv(s, map[string]string{"LAB_DB_PASSWORD": "hunter2"})
	ref, err := s.ResolvePlan(bg, "provision-lab", "")
	require.NoError(t, err)

	set, err := s.ResolveVars(bg, ref, VarOptions{From: "last", Flags: []string{"compute_nodes=5"}})
	require.NoError(t, err)

	assert.Equal(t, "PROJ-PROV12-8", set.FromBuild)
	assert.True(t, set.DeclaredKnown)
	assert.Equal(t, map[string]string{
		"cluster_name":  "beta",
		"cluster_type":  "dcos",
		"compute_nodes": "5",
		"db_password":   "hunter2",
	}, set.Changed(), "global_flag is not declared and not in the target, so --from skips it")
	assert.Equal(t, map[string]bool{"db_password": true}, set.Secret())

	src := map[string]string{}
	for _, v := range set.Vars {
		src[v.Name] = v.Source
	}
	assert.Equal(t, map[string]string{"cluster_name": "from #8", "cluster_type": "from #8", "compute_nodes": "flag", "db_password": "env"}, src)

	pw, _ := set.Get("db_password")
	assert.Equal(t, MaskedDisplay, pw.Display())
	assert.Contains(t, set.Warnings[0], "db_password is masked in PROJ-PROV12-8")
	names := []string{}
	for _, v := range set.Vars {
		names = append(names, v.Name)
	}
	assert.IsIncreasing(t, names, "sorted by name")
}

func TestResolveVarsOnlySendsChanges(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, err := s.ResolvePlan(bg, "PROJ-PROV", "")
	require.NoError(t, err)
	set, err := s.ResolveVars(bg, ref, VarOptions{Flags: []string{"cluster_type=k8s"}})
	require.NoError(t, err)
	assert.Empty(t, set.Changed(), "k8s is already the plan value; the masked password is untouched")
}

func TestResolveVarsValidationOrder(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, _ := s.ResolvePlan(bg, "provision-lab", "")

	_, err := s.ResolveVars(bg, ref, VarOptions{})
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), "LAB_DB_PASSWORD", "unset env reference is reported first")
	assert.Contains(t, err.Error(), "db_password")

	withEnv(s, map[string]string{"LAB_DB_PASSWORD": "x"})
	_, err = s.ResolveVars(bg, ref, VarOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster_name is required by preset provision-lab")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "--var cluster_name=VALUE", e.Try)

	_, err = s.ResolveVars(bg, ref, VarOptions{Flags: []string{"Cluster_Name=a"}})
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "cluster_name is required by preset provision-lab", e.What)
	assert.Equal(t, "Cluster_Name is set, but names are case-sensitive", e.Why)
	assert.Equal(t, "--var cluster_name=a", e.Try)

	_, err = s.ResolveVars(bg, ref, VarOptions{Flags: []string{"cluster_name=a", "cluster_type=nomad"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `cluster_type="nomad" is not allowed by preset provision-lab`)
	assert.Contains(t, err.Error(), "allowed: k8s, dcos")
	require.ErrorAs(t, err, &e)
	assert.Empty(t, e.Try)

	_, err = s.ResolveVars(bg, ref, VarOptions{Flags: []string{"cluster_name=a", "cluster_type=k8"}})
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "did you mean --var cluster_type=k8s?", e.Try)
}

func TestResolveVarsWarnsOnUndeclaredFlag(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, _ := s.ResolvePlan(bg, "PROJ-PROV", "")
	set, err := s.ResolveVars(bg, ref, VarOptions{Flags: []string{"cluster_nme=y", "cluster_nme=x"}})
	require.NoError(t, err)
	require.Len(t, set.Warnings, 1, "a repeated name warns once")
	assert.Contains(t, set.Warnings[0], "cluster_nme is not a declared variable of PROJ-PROV")
	assert.Contains(t, set.Warnings[0], "did you mean cluster_name")
	assert.Equal(t, "x", set.Changed()["cluster_nme"], "still sent: it may override a global variable")
}

func TestResolveVarsBadFlag(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, _ := s.ResolvePlan(bg, "PROJ-PROV", "")
	_, err := s.ResolveVars(bg, ref, VarOptions{Flags: []string{"novalue"}})
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
}

func TestResolveVarsWithoutDeclaredList(t *testing.T) {
	p := fakeBamboo()
	p.VariablesErr = errs.Bamboof("plan variables cannot be listed").Wrap(errs.ErrUnsupported)
	s := newService(t, p)
	ref, _ := s.ResolvePlan(bg, "PROJ-PROV", "develop")
	set, err := s.ResolveVars(bg, ref, VarOptions{From: "8"})
	require.NoError(t, err)
	assert.False(t, set.DeclaredKnown)
	assert.Equal(t, "x", set.Changed()["global_flag"], "without a declared list every build variable is taken")
	_, masked := set.Changed()["db_password"]
	assert.False(t, masked, "a masked value is never sent back")
	assert.Contains(t, set.Warnings[len(set.Warnings)-1], "took every variable of PROJ-PROV12-8")
}

func TestResolveVarsFromUnsupported(t *testing.T) {
	p := fakeBamboo()
	p.BuildVarsErr = errs.Bamboof("no").Wrap(errs.ErrUnsupported)
	s := newService(t, p)
	ref, _ := s.ResolvePlan(bg, "PROJ-PROV", "develop")
	_, err := s.ResolveVars(bg, ref, VarOptions{From: "last"})
	require.Error(t, err)
	assert.Equal(t, errs.KindBamboo, errs.KindOf(err))
	assert.Contains(t, err.Error(), "cannot reuse variables of PROJ-PROV12-8")
}

func TestOptionsErrorNeverShowsSecret(t *testing.T) {
	s := newService(t, fakeBamboo())
	withEnv(s, map[string]string{"LAB_DB_PASSWORD": "hunter2"})
	tgt := s.Cfg.Project.Targets["provision-lab"]
	tgt.Options = config.StringListMap{"db_password": {"a", "b"}}
	s.Cfg.Project.Targets["provision-lab"] = tgt
	ref, err := s.ResolvePlan(bg, "provision-lab", "")
	require.NoError(t, err)

	_, err = s.ResolveVars(bg, ref, VarOptions{Flags: []string{"cluster_name=x"}})
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), `db_password="********" is not allowed`)
	assert.NotContains(t, err.Error(), "hunter2")
}

func TestPlanVars(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, _ := s.ResolvePlan(bg, "PROJ-PROV", "develop")
	res, err := s.PlanVars(bg, ref, "")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV12-8", res.FromBuild)
	assert.Equal(t, []VarRow{
		{Name: "cluster_type", Value: "k8s", LastUsed: "dcos"},
		{Name: "compute_nodes", Value: "1", LastUsed: "3"},
		{Name: "cluster_name", Value: "", LastUsed: "beta"},
		{Name: "db_password", Value: "********", LastUsed: "********", Masked: true},
	}, res.Rows)
}

// TestValidateVarsNeedsNoService is the point of the split: the terminal UI's
// run form calls it on every keystroke, so it must touch no provider, no
// clock and no network.
func TestValidateVarsNeedsNoService(t *testing.T) {
	base := VarSet{DeclaredKnown: true, Declared: map[string]bool{"cluster_name": true, "cluster_type": true},
		Vars: []ResolvedVar{
			{Name: "cluster_name", Declared: true},
			{Name: "cluster_type", Value: "k8s", PlanValue: "k8s", Source: "plan", Declared: true},
		}}
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV"}

	got, err := ValidateVars(ref, base, []string{"cluster_name=beta"}, func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"cluster_name": "beta"}, got.Changed())
}

func TestValidateVarsEnforcesRequired(t *testing.T) {
	tgt := config.ResolvedTarget{Name: "provision-lab", Target: config.Target{Required: []string{"cluster_name"}}}
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV", Target: &tgt}
	_, err := ValidateVars(ref, VarSet{DeclaredKnown: true}, nil, func(string) string { return "" })
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), "cluster_name")
}

func TestValidateVarsEnforcesOptions(t *testing.T) {
	tgt := config.ResolvedTarget{Name: "provision-lab",
		Target: config.Target{Options: config.StringListMap{"cluster_type": {"k8s", "dcos"}}}}
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV", Target: &tgt}
	_, err := ValidateVars(ref, VarSet{DeclaredKnown: true}, []string{"cluster_type=swarm"}, func(string) string { return "" })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "k8s, dcos")
}

func TestValidateVarsResolvesEnvReferencesAndMarksThemSecret(t *testing.T) {
	tgt := config.ResolvedTarget{Name: "provision-lab",
		Target: config.Target{Defaults: config.StringMap{"token": "${LAB_TOKEN}"}}}
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV", Target: &tgt}
	base := VarSet{DeclaredKnown: true, Vars: []ResolvedVar{
		{Name: "token", Value: "${LAB_TOKEN}", Source: "preset"},
	}}

	got, err := ValidateVars(ref, base, nil, func(k string) string {
		if k == "LAB_TOKEN" {
			return "s3cret"
		}
		return ""
	})
	require.NoError(t, err)
	tok, ok := got.Get("token")
	require.True(t, ok)
	assert.Equal(t, MaskedDisplay, tok.Display(), "a resolved environment value is secret")

	_, err = ValidateVars(ref, base, nil, func(string) string { return "" })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LAB_TOKEN")
}

// TestVarBaseFetchesWithoutApplyingRules: a required variable left empty is
// not an error until ValidateVars runs.
func TestVarBaseFetchesWithoutApplyingRules(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, err := s.ResolvePlan(bg, "provision-lab", "")
	require.NoError(t, err)

	base, err := s.VarBase(bg, ref, "")
	require.NoError(t, err, "cluster_name is required and empty, and VarBase does not care")
	assert.True(t, base.DeclaredKnown)
	assert.NotEmpty(t, base.Vars)

	_, err = ValidateVars(ref, base, nil, func(string) string { return "" })
	require.Error(t, err, "the rules are what reject it")
}

// TestVarBaseAndValidateVarsComposeToResolveVars pins the refactor: the two
// halves in sequence are the whole.
func TestVarBaseAndValidateVarsComposeToResolveVars(t *testing.T) {
	s := newService(t, fakeBamboo())
	withEnv(s, map[string]string{"LAB_DB_PASSWORD": "hunter2"})
	ref, err := s.ResolvePlan(bg, "provision-lab", "")
	require.NoError(t, err)

	want, err := s.ResolveVars(bg, ref, VarOptions{From: "last", Flags: []string{"compute_nodes=5"}})
	require.NoError(t, err)

	base, err := s.VarBase(bg, ref, "last")
	require.NoError(t, err)
	got, err := ValidateVars(ref, base, []string{"compute_nodes=5"}, s.Getenv)
	require.NoError(t, err)

	assert.Equal(t, want.Changed(), got.Changed())
	assert.Equal(t, want.Secret(), got.Secret())
	assert.Equal(t, want.FromBuild, got.FromBuild)
}

// TestVarBaseMarksAnEnvRefTargetDefaultSecret pins the rule for a name the
// heuristic does not catch. The run form builds its fields from VarBase,
// before ValidateVars resolves anything, so a base that leaves token's
// ${LAB_TOKEN} non-secret puts the reference on screen and then echoes what
// is typed over it.
func TestVarBaseMarksAnEnvRefTargetDefaultSecret(t *testing.T) {
	s := newService(t, fakeBamboo())
	withEnv(s, map[string]string{"LAB_TOKEN": "shhh"})
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV", Target: &config.ResolvedTarget{
		Name: "lab",
		Target: config.Target{Plan: "PROJ-PROV", Defaults: config.StringMap{
			"token":        "${LAB_TOKEN}",
			"cluster_type": "dcos",
		}},
	}}

	base, err := s.VarBase(bg, ref, "")
	require.NoError(t, err)

	tok, ok := base.Get("token")
	require.True(t, ok)
	assert.True(t, tok.Secret, "an ${ENV} target default is secret whatever its name looks like")
	assert.Equal(t, MaskedDisplay, tok.Display())

	// A plain literal default with an innocent name stays visible.
	ct, ok := base.Get("cluster_type")
	require.True(t, ok)
	assert.False(t, ct.Secret)
	assert.Equal(t, "dcos", ct.Display())
}

func emptyEnv(string) string { return "" }

// TestApplyVarsChecksNoRules: the edit buffer opens on this set, so an empty
// required name and an unset ${ENV} must not be errors here.
func TestApplyVarsChecksNoRules(t *testing.T) {
	tgt := config.ResolvedTarget{Name: "provision-lab", Target: config.Target{
		Required: []string{"cluster_name"},
		Defaults: config.StringMap{"token": "${LAB_TOKEN}"},
	}}
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV", Target: &tgt}
	base := VarSet{DeclaredKnown: true, Declared: map[string]bool{"cluster_type": true},
		Vars: []ResolvedVar{
			{Name: "cluster_type", Value: "k8s", PlanValue: "k8s", Source: "plan", Declared: true},
			{Name: "token", Value: "${LAB_TOKEN}", Source: "preset", Secret: true},
		}}

	got, err := ApplyVars(ref, base, []string{"cluster_type=dcos"})
	require.NoError(t, err)
	ct, _ := got.Get("cluster_type")
	assert.Equal(t, "dcos", ct.Value)
	assert.Equal(t, "flag", ct.Source)
	tok, _ := got.Get("token")
	assert.Equal(t, "${LAB_TOKEN}", tok.Value, "ApplyVars resolves nothing")

	_, err = ApplyVars(ref, base, []string{"novalue"})
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
}

func TestValidateEditedAnEditBeatsAFlag(t *testing.T) {
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV"}
	base := VarSet{DeclaredKnown: true, Declared: map[string]bool{"cluster_name": true},
		Vars: []ResolvedVar{{Name: "cluster_name", Source: "plan", Declared: true}}}

	got, err := ValidateEdited(ref, base, []string{"cluster_name=alpha"},
		[]Edit{{Name: "cluster_name", Value: "beta"}}, emptyEnv)
	require.NoError(t, err)
	v, _ := got.Get("cluster_name")
	assert.Equal(t, "beta", v.Value)
	assert.Equal(t, "edit", v.Source)
	assert.Equal(t, map[string]string{"cluster_name": "beta"}, got.Changed())
}

func TestValidateEditedResolvesAnEditedEnvReference(t *testing.T) {
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV"}
	base := VarSet{DeclaredKnown: true, Declared: map[string]bool{"token": true},
		Vars: []ResolvedVar{{Name: "token", Source: "plan", Declared: true}}}
	env := func(k string) string {
		if k == "LAB_TOKEN" {
			return "s3cret"
		}
		return ""
	}

	got, err := ValidateEdited(ref, base, nil, []Edit{{Name: "token", Value: "${LAB_TOKEN}"}}, env)
	require.NoError(t, err)
	tok, _ := got.Get("token")
	assert.Equal(t, "s3cret", tok.Value)
	assert.Equal(t, "env", tok.Source)
	assert.True(t, tok.Secret, "a value read from the environment is secret")

	_, err = ValidateEdited(ref, base, nil, []Edit{{Name: "token", Value: "${LAB_TOKEN}"}}, emptyEnv)
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), "LAB_TOKEN")
}

// TestValidateEditedLeavesAFlagEnvReferenceLiteral pins today's --var
// behaviour: the shell expands references, bam does not.
func TestValidateEditedLeavesAFlagEnvReferenceLiteral(t *testing.T) {
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV"}
	base := VarSet{DeclaredKnown: true, Declared: map[string]bool{"token": true},
		Vars: []ResolvedVar{{Name: "token", Source: "plan", Declared: true}}}
	got, err := ValidateEdited(ref, base, []string{"token=${LAB_TOKEN}"}, nil, emptyEnv)
	require.NoError(t, err)
	tok, _ := got.Get("token")
	assert.Equal(t, "${LAB_TOKEN}", tok.Value)
	assert.Equal(t, "flag", tok.Source)
}

func TestValidateEditedAppliesTheRulesToEdits(t *testing.T) {
	tgt := config.ResolvedTarget{Name: "provision-lab", Target: config.Target{
		Required: []string{"cluster_name"},
		Options:  config.StringListMap{"cluster_type": {"k8s", "dcos"}},
	}}
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV", Target: &tgt}
	base := VarSet{DeclaredKnown: true, Declared: map[string]bool{"cluster_name": true, "cluster_type": true}}

	_, err := ValidateEdited(ref, base, []string{"cluster_name=a"}, []Edit{{Name: "cluster_type", Value: "swarm"}}, emptyEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `cluster_type="swarm" is not allowed by preset provision-lab`)

	_, err = ValidateEdited(ref, base, []string{"cluster_name=a"}, []Edit{{Name: "cluster_name", Value: ""}}, emptyEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster_name is required by preset provision-lab")
}

func TestValidateEditedWarnsOnceForAnUndeclaredName(t *testing.T) {
	ref := PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV"}
	base := VarSet{DeclaredKnown: true, Declared: map[string]bool{"cluster_name": true}}

	got, err := ValidateEdited(ref, base, []string{"clustr_name=a"},
		[]Edit{{Name: "clustr_name", Value: "b"}, {Name: "extra", Value: "x"}}, emptyEnv)
	require.NoError(t, err)
	require.Len(t, got.Warnings, 2, "clustr_name was already warned about as a --var")
	assert.Contains(t, got.Warnings[0], "clustr_name is not a declared variable of PROJ-PROV")
	assert.Contains(t, got.Warnings[1], "extra is not a declared variable of PROJ-PROV")
	assert.Equal(t, "b", got.Changed()["clustr_name"])
}

func TestResolveVarsMalformedFlagShowsTheForm(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, err := s.ResolvePlan(bg, "PROJ-PROV", "")
	require.NoError(t, err)
	_, err = s.ResolveVars(bg, ref, VarOptions{Flags: []string{"cluster_type"}})
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Equal(t, "--var cluster_type=VALUE", e.Try)
}
