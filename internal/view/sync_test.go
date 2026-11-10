package view

import (
	"errors"
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func syncResults() []SyncResult {
	return []SyncResult{
		{Sync: app.SyncPlan{Name: "provision-lab", Plan: "PROJ-PROV", File: "/home/jdoe/src/repo/.bam.yaml",
			Added: []config.DraftVar{
				{Name: "region", Value: "eu-west-1", Comment: "plan default"},
				{Name: "api_token", Value: "${API_TOKEN}", Comment: "masked by Bamboo; set env var API_TOKEN"},
				{Name: "db_password", Value: "hunter2", Comment: "plan default"},
			},
			Stale: []string{"old_flag"}},
			File: ".bam.yaml", Status: SyncSynced, Redeclared: []string{"zone"}},
		{Sync: app.SyncPlan{Name: "build", Plan: "PROJ-BUILD", Stale: []string{}}, File: ".bam.yaml", Status: SyncUpToDate},
		{Sync: app.SyncPlan{Name: "lab", Plan: "LAB-PROV", Stale: []string{}}, Status: SyncError, Err: errors.New("plan LAB-PROV not found")},
	}
}

func TestSyncText(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, Sync(o, syncResults(), false))
	assert.Equal(t, `provision-lab  PROJ-PROV  .bam.yaml
  +  region       "eu-west-1"     plan default
  +  api_token    "${API_TOKEN}"  masked by Bamboo; set env var API_TOKEN
  +  db_password  "********"      plan default
  !  old_flag                     not declared on PROJ-PROV (kept)
  ~  zone                         declared again on PROJ-PROV (marker removed)
build  PROJ-BUILD  .bam.yaml  up to date
lab  LAB-PROV  error: plan LAB-PROV not found
`, buf.String())
}

func TestSyncTextDryRun(t *testing.T) {
	o, buf := testOut(false)
	rs := syncResults()[:2]
	rs[0].Status = SyncWouldSync
	require.NoError(t, Sync(o, rs, true))
	assert.Contains(t, buf.String(), "would sync: provision-lab  PROJ-PROV  .bam.yaml\n")
	assert.Contains(t, buf.String(), "build  PROJ-BUILD  .bam.yaml  up to date\n")
	assert.Contains(t, buf.String(), "dry run: no files written\n")
}

func TestSyncJSON(t *testing.T) {
	docs := SyncJSON(syncResults())
	require.Len(t, docs, 3)
	assert.Equal(t, SyncDoc{Name: "provision-lab", PlanKey: "PROJ-PROV", File: "/home/jdoe/src/repo/.bam.yaml", Status: "synced",
		Added: []SyncVarDoc{
			{Name: "region", Value: "eu-west-1", Comment: "plan default"},
			{Name: "api_token", Value: "${API_TOKEN}", Comment: "masked by Bamboo; set env var API_TOKEN"},
			{Name: "db_password", Value: "********", Comment: "plan default"},
		},
		Stale: []string{"old_flag"}, Redeclared: []string{"zone"}}, docs[0])
	assert.Equal(t, []SyncVarDoc{}, docs[1].Added)
	assert.Equal(t, []string{}, docs[1].Redeclared)
	assert.Equal(t, "error", docs[2].Status)
	assert.Equal(t, "plan LAB-PROV not found", docs[2].Error)
}
