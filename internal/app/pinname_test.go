package app

import (
	"context"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (pin by name): a name finds one plan, by an exact name first, then
// by the words it holds; none or several fits is a usage error that says so.
// The server's plan search is used when it has one, the configured projects'
// plans when it has none.

func namedBamboo() *fake.Provider {
	return &fake.Provider{Plans: map[string][]provider.Plan{
		"PROJ": {
			{Key: "PROJ-INTEG", Name: "Integrate for next release", ProjectKey: "PROJ"},
			{Key: "PROJ-INTEGX", Name: "Integrate for next release extra", ProjectKey: "PROJ"},
			{Key: "PROJ-TESTS", Name: "Run feature tests", ProjectKey: "PROJ"},
			{Key: "PROJ-LOAD", Name: "Run feature tests nightly", ProjectKey: "PROJ"},
		},
		"OPS": {{Key: "OPS-DEPLOY", Name: "Deploy package", ProjectKey: "OPS"}},
	}}
}

func findName(t *testing.T, p *fake.Provider, name string) (provider.Plan, error) {
	t.Helper()
	svc := newService(t, p)
	return svc.FindPlanByName(context.Background(), name)
}

func TestFindPlanByNameExactNameWinsOverLongerNames(t *testing.T) {
	plan, err := findName(t, namedBamboo(), "integrate FOR next release")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-INTEG", plan.Key, "the plan with exactly that name, ignoring case, not its longer sibling")
}

func TestFindPlanByNameTakesAPartOfTheName(t *testing.T) {
	plan, err := findName(t, namedBamboo(), "deploy")
	require.NoError(t, err)
	assert.Equal(t, "OPS-DEPLOY", plan.Key)
	assert.Equal(t, "Deploy package", plan.Name)
}

func TestFindPlanByNameWordsMayBeApartInTheName(t *testing.T) {
	plan, err := findName(t, namedBamboo(), "NIGHTLY tests")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-LOAD", plan.Key)
}

func TestFindPlanByNameSeveralFitsListedWithKeyAndName(t *testing.T) {
	_, err := findName(t, namedBamboo(), "run feature")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Contains(t, e.What, "fits 2 plans")
	assert.Contains(t, e.Why, "PROJ-TESTS  Run feature tests")
	assert.Contains(t, e.Why, "PROJ-LOAD  Run feature tests nightly")
}

func TestFindPlanByNameListsAtMostTenFits(t *testing.T) {
	p := &fake.Provider{Plans: map[string][]provider.Plan{"PROJ": {}}}
	for i := range 13 {
		key := "PROJ-P" + string(rune('A'+i))
		p.Plans["PROJ"] = append(p.Plans["PROJ"], provider.Plan{Key: key, Name: "Nightly job " + key, ProjectKey: "PROJ"})
	}
	_, err := findName(t, p, "nightly")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.What, "fits 13 plans")
	assert.Contains(t, e.Why, "PROJ-PJ")
	assert.NotContains(t, e.Why, "PROJ-PK")
	assert.Contains(t, e.Why, "and 3 more")
}

func TestFindPlanByNameNoFit(t *testing.T) {
	_, err := findName(t, namedBamboo(), "no such pipeline")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Contains(t, e.What, `no plan is named or has a name containing "no such pipeline"`)
	assert.Contains(t, e.Try, "bam plan list")
}

func TestFindPlanByNameReadsConfiguredProjectsWhenTheServerHasNoSearch(t *testing.T) {
	p := namedBamboo()
	p.NoPlanSearch = true
	svc := newService(t, p) // the test config scopes the server to PROJ
	plan, err := svc.FindPlanByName(context.Background(), "nightly")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-LOAD", plan.Key)
	assert.Equal(t, []string{"nightly"}, p.SearchCalls, "the search is asked once, then the projects are read")
}

func TestFindPlanByNameFallsBackToProjectsWhenTheSearchFindsNothing(t *testing.T) {
	p := namedBamboo()
	svc := newService(t, p)
	// The fake's search matches a whole phrase; the words are apart in the name.
	plan, err := svc.FindPlanByName(context.Background(), "extra next integrate")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-INTEGX", plan.Key)
}

// A search page fills with plans whose project's name matches; the plan
// named exactly that is past the page, in a configured project.
func TestFindPlanByNameAPartialSearchPageAlsoReadsTheProjects(t *testing.T) {
	p := namedBamboo()
	p.Plans["PROJ"] = append(p.Plans["PROJ"], provider.Plan{Key: "PROJ-DEPLOY", Name: "Deploy", ProjectKey: "PROJ"})
	p.SearchPage = []provider.Plan{
		{Key: "OPS-DEPLOY", Name: "Deploy package", ProjectKey: "OPS"},
		{Key: "OPS-WEB", Name: "Web", ProjectKey: "OPS"},
	}
	plan, err := findName(t, p, "deploy")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-DEPLOY", plan.Key, "the exact name beats a part on the partial page")
}

func TestFindPlanByNameSaysAPartialSearchMayHoldMoreFits(t *testing.T) {
	p := namedBamboo()
	p.SearchPage = []provider.Plan{
		{Key: "OPS-A", Name: "Nightly A", ProjectKey: "OPS"},
		{Key: "OPS-B", Name: "Nightly B", ProjectKey: "OPS"},
	}
	_, err := findName(t, p, "nightly")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.What, "fits 3 plans or more", "the two on the page and PROJ-LOAD from the project")

	p.SearchPage = []provider.Plan{{Key: "OPS-WEB", Name: "Web", ProjectKey: "OPS"}}
	_, err = findName(t, p, "zzz")
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.Why, "give more of the name")
}

// The search page holds one plan with the word, but more may follow it, and
// the plan named exactly that may be among them: one fit is not proof.
func TestFindPlanByNameOnePartFitOnAPartialPageIsNotPinned(t *testing.T) {
	p := &fake.Provider{Plans: map[string][]provider.Plan{"PROJ": {}}}
	p.SearchPage = []provider.Plan{
		{Key: "OPS-DEPLOYPKG", Name: "Deploy package", ProjectKey: "OPS"},
		{Key: "OPS-WEB", Name: "Web", ProjectKey: "OPS"},
	}
	_, err := findName(t, p, "deploy")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Contains(t, e.What, `"deploy" fits 1 plan or more`)
	assert.Contains(t, e.Why, "OPS-DEPLOYPKG  Deploy package")
	assert.Contains(t, e.Try, "give more of its name")
}

func TestFindPlanByNameAnExactFitOnAPartialPageIsPinned(t *testing.T) {
	p := &fake.Provider{Plans: map[string][]provider.Plan{"PROJ": {}}}
	p.SearchPage = []provider.Plan{{Key: "OPS-DEPLOY", Name: "Deploy", ProjectKey: "OPS"}}
	plan, err := findName(t, p, "DEPLOY")
	require.NoError(t, err)
	assert.Equal(t, "OPS-DEPLOY", plan.Key)
}
