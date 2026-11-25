package app

import (
	"testing"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func repoFields(f PresetForm) map[string]FormField {
	out := map[string]FormField{}
	for _, fl := range f.Fields {
		out[fl.Key] = fl
	}
	return out
}

// Removing a saved repository drops it from the preset; removing one the form
// just added changes nothing.
func TestPresetEditFormRemovedRepositoryIsDropped(t *testing.T) {
	tt := savedPreset()
	tt.Repositories = config.StringMap{"api": "main", "web": "dev"}
	f := NewPresetEditForm(tt, nil)
	for i := range f.Fields {
		if f.Fields[i].Key == "repositories.api" {
			f.Fields[i].Removed = true
		}
	}
	e := f.Edit()
	require.NotNil(t, e.Repositories)
	assert.Equal(t, []config.RepoBranch{{Name: "web", Branch: "dev"}}, *e.Repositories)

	f = NewPresetEditForm(tt, nil)
	f.SetRepository("docs", "main")
	f.Fields[len(f.Fields)-3].Removed = true // the added repository's row
	assert.Nil(t, f.Edit().Repositories, "an added row that is removed again changes nothing")
}

func TestPresetEditFormAddsARepositoryAfterTheOthers(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), nil)
	f.SetRepository("docs", "main")
	var keys []string
	for _, fl := range f.Fields {
		keys = append(keys, fl.Key)
	}
	assert.Equal(t, []string{"name", "branch", "repositories.api", "repositories.docs", "required", "timeout"}, keys)
	e := f.Edit()
	require.NotNil(t, e.Repositories)
	assert.Equal(t, []config.RepoBranch{{Name: "api", Branch: "main"}, {Name: "docs", Branch: "main"}}, *e.Repositories)
}

// A new branch brings the repositories Bamboo lists for it: listed ones take
// its branch, a saved one it does not list is removed, an added one it does not
// list is dropped.
func TestUseRepositoriesReplacesWhatTheBranchLists(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), nil)
	f.SetRepository("added", "x")
	f.UseRepositories([]RepoBranch{{Name: "scripts", Branch: "develop"}})
	got := repoFields(f)
	assert.True(t, got["repositories.api"].Removed, "api was saved and the new branch does not list it")
	assert.NotContains(t, got, "repositories.added")
	assert.Equal(t, "develop", got["repositories.scripts"].Value)
	assert.Equal(t, "", got["repositories.scripts"].Orig)
	e := f.Edit()
	assert.Equal(t, []config.RepoBranch{{Name: "scripts", Branch: "develop"}}, *e.Repositories)
	require.NoError(t, f.Validate())
}

func TestPresetFormValidateRepositories(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), nil)
	for i := range f.Fields {
		if f.Fields[i].Key == "repositories.api" {
			f.Fields[i].Value = ""
		}
	}
	assert.ErrorContains(t, f.Validate(), "needs a branch")
	for i := range f.Fields {
		if f.Fields[i].Key == "repositories.api" {
			f.Fields[i].Removed = true
		}
		if f.Fields[i].Key == "branch" {
			f.Fields[i].Value = ""
		}
	}
	assert.NoError(t, f.Validate(), "a removed repository needs no branch, and none needs the plan branch")
}

// A repository only a lower file holds cannot be removed here.
func TestPresetFormValidateRefusesRemovingARepositoryAnotherFileHolds(t *testing.T) {
	f := NewPresetEditForm(savedPreset(), nil)
	f.Path = "top.yaml"
	f.Held = Held{Only: map[string]string{"repositories.api": "low.yaml"}, Also: map[string]string{}}
	require.NoError(t, f.Validate())
	for i := range f.Fields {
		if f.Fields[i].Key == "repositories.api" {
			f.Fields[i].Removed = true
		}
	}
	assert.ErrorContains(t, f.Validate(), "low.yaml")
}

func TestPlanBranchRepositoriesReadsTheNewestFinishedBuild(t *testing.T) {
	s := newService(t, repoBuilds())
	got, err := s.PlanBranchRepositories(bg, "PROJ-PARENT", "tst")
	require.NoError(t, err)
	assert.Equal(t, []RepoBranch{{Name: "app", Branch: "develop"}, {Name: "scripts", Branch: "feature/cli-tools"}}, got)

	got, err = s.PlanBranchRepositories(bg, "PROJ-PARENT", "")
	require.NoError(t, err)
	assert.Empty(t, got, "no branch, no repositories")

	_, err = s.PlanBranchRepositories(bg, "PROJ-PARENT", "no-such-branch")
	assert.Error(t, err)
}

// A plan branch with no finished job lists repository names but no branch to
// offer; the form gets none of them rather than a blank branch it would refuse.
func TestPlanBranchRepositoriesLeavesOutAnUnbuiltRepository(t *testing.T) {
	p := repoBuilds()
	unbuilt := jb("PROJ-PARENT1-4", provider.StateSuccess, nil)
	unbuilt.Revisions = []provider.Revision{{Repository: "app"}}
	p.History["PROJ-PARENT1"] = []provider.Build{unbuilt}
	got, err := newService(t, p).PlanBranchRepositories(bg, "PROJ-PARENT", "tst")
	require.NoError(t, err)
	assert.Empty(t, got)
}
