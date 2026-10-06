package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// writeTrain writes a train file "hotfix" with publish-specs then
// provision-lab, and returns its path.
func writeTrain(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "hotfix.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

const hotfixBody = "stages:\n  - [publish-specs]\n  - [{target: provision-lab, vars: {cluster_name: alpha}}]\n"

func TestPlanSetFromAFile(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), hotfixBody)
	p, err := svc.PlanSet(context.Background(), "ignored", SetOptions{File: path})
	require.NoError(t, err)
	require.Equal(t, "hotfix", p.Name)
	require.Equal(t, path, p.File)
	require.Len(t, p.Stages, 2)
}

func TestRunSetRecordsTheFile(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), hotfixBody)
	p, err := svc.PlanSet(context.Background(), "", SetOptions{File: path})
	require.NoError(t, err)
	id, ch, err := svc.RunSet(context.Background(), p, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	rec, err := svc.Sets.Load(id)
	require.NoError(t, err)
	require.Equal(t, path, rec.File)
	require.Equal(t, "hotfix", rec.Set)
	require.Equal(t, SetSuccessful, rec.Status)
}

// fileRecord saves an interrupted run of the train file at path.
func fileRecord(t *testing.T, svc *Service, path string) SetRecord {
	rec := saveRecord(t, svc, SetRecord{Stages: twoStages("PROJ-BUILD-1")})
	rec.Set, rec.File = "hotfix", path
	rec.ID = NewSetID("hotfix", svc.Clock.Now())
	require.NoError(t, svc.Sets.Save(rec))
	return rec
}

func TestResumeAFileSetRereadsTheFile(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), hotfixBody)
	rec := fileRecord(t, svc, path)
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
	require.Len(t, fp.Triggered, 1, "only the run that never triggered")
}

func TestResumeAFileSetWhoseFileIsGone(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	dir := t.TempDir()
	path := writeTrain(t, dir, hotfixBody)
	rec := fileRecord(t, svc, path)
	require.NoError(t, os.Remove(path))
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "the train file of set run "+rec.ID+" is gone")
	require.Contains(t, errTry(err), "--file")

	moved := writeTrain(t, t.TempDir(), hotfixBody)
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{File: moved}, SetLockOptions{})
	require.NoError(t, err, "--file points at the moved file")
	drain(ch)
}

func TestResumeAFileThatNamesAnotherSet(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), "name: other\n"+hotfixBody)
	rec := fileRecord(t, svc, path)
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "defines set other, not hotfix")
}

func TestResumeAFileSetWhoseStagesChanged(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), "stages:\n  - [publish-specs]\n")
	rec := fileRecord(t, svc, path)
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "changed since")
}
