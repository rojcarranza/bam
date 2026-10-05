package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func decodeRun(t *testing.T, src string) SetRun {
	t.Helper()
	var st Stage
	require.NoError(t, yaml.Unmarshal([]byte("["+src+"]"), &st))
	require.Len(t, st, 1)
	return st[0]
}

func names(rs []ExpandedRun) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func TestExpandWithoutAMatrixIsTheRunItself(t *testing.T) {
	rs := decodeRun(t, "{target: deploy, vars: {env: qa}}").Expand()
	require.Equal(t, []ExpandedRun{{Name: "deploy", Target: "deploy", Vars: StringMap{"env": "qa"}}}, rs)
}

func TestExpandProductOrder(t *testing.T) {
	r := decodeRun(t, "{target: deploy, matrix: {region: [eu, us], env: [qa, staging]}}")
	require.Equal(t, []string{
		"deploy[env=qa,region=eu]", "deploy[env=qa,region=us]",
		"deploy[env=staging,region=eu]", "deploy[env=staging,region=us]",
	}, names(r.Expand()), "sorted keys, values as written, last key fastest")
}

func TestExpandListKeepsOrderAndOwnKeys(t *testing.T) {
	r := decodeRun(t, "{target: smoke, vars: {tier: web}, matrix: [{env: prod, region: us}, {env: qa}]}")
	rs := r.Expand()
	require.Equal(t, []string{"smoke[env=prod,region=us]", "smoke[env=qa]"}, names(rs))
	require.Equal(t, StringMap{"tier": "web", "env": "qa"}, rs[1].Vars, "vars stay, matrix values join")
}

func TestExpandMatrixBeatsVars(t *testing.T) {
	rs := decodeRun(t, "{target: deploy, vars: {env: dev}, matrix: [{env: qa}]}").Expand()
	require.Equal(t, "qa", rs[0].Vars["env"])
}

func TestExpandMasksASecretInTheName(t *testing.T) {
	rs := decodeRun(t, `{target: deploy, matrix: [{db_password: hunter2, env: qa}, {db_password: "${PW}", env: prod}]}`).Expand()
	require.Equal(t, []string{"deploy[db_password=********,env=qa]", "deploy[db_password=${PW},env=prod]"}, names(rs))
	require.Equal(t, "hunter2", rs[0].Vars["db_password"], "the value itself is still sent")
}

func TestMatrixDecodeErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"scalar":        {"{target: d, matrix: qa}", "matrix"},
		"empty map":     {"{target: d, matrix: {}}", "matrix"},
		"empty list":    {"{target: d, matrix: []}", "matrix"},
		"empty values":  {"{target: d, matrix: {env: []}}", `"env"`},
		"nested value":  {"{target: d, matrix: {env: [[qa]]}}", `"env"`},
		"list of lists": {"{target: d, matrix: [[qa]]}", "matrix"},
	} {
		t.Run(name, func(t *testing.T) {
			var st Stage
			err := yaml.Unmarshal([]byte("["+tc.src+"]"), &st)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}
