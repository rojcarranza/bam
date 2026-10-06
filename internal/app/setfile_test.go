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

// --file is for a train file's run; a configured set's run must not quietly
// take its definition from a file.
func TestResumeAConfiguredSetRefusesFile(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Stages: twoStages("PROJ-BUILD-1")})
	path := writeTrain(t, t.TempDir(), "name: release\n"+hotfixBody)
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{File: path}, SetLockOptions{})
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
	require.Contains(t, err.Error(), "is a configured set")
}

// Resuming from a moved file remembers where it is now.
func TestResumeFromAMovedFileRemembersTheNewPath(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), hotfixBody)
	rec := fileRecord(t, svc, path)
	require.NoError(t, os.Remove(path))
	moved := writeTrain(t, t.TempDir(), hotfixBody)
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{File: moved}, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	got, err := svc.Sets.Load(rec.ID)
	require.NoError(t, err)
	require.Equal(t, moved, got.File)
}

func TestAChangedFileSetSuggestsRunningTheFile(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), "stages:\n  - [publish-specs]\n")
	rec := fileRecord(t, svc, path)
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.Contains(t, errTry(err), "bam set run -f "+path)
}

// A --file that does not exist either gets the same recovery hint as a
// recorded file that is gone.
func TestResumeWithAMissingFileFlagKeepsTheRecoveryHint(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), hotfixBody)
	rec := fileRecord(t, svc, path)
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{File: missing}, SetLockOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "is gone: "+missing)
	require.Contains(t, errTry(err), "--file")
}

// A train file without name: is named after its file. Moving it under
// another file name must not stop its run from resuming: the record's name
// stands in. Only a file whose own name: says another set is refused.
func TestResumeFromARenamedFileWithoutAName(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), hotfixBody)
	rec := fileRecord(t, svc, path)
	renamed := filepath.Join(t.TempDir(), "moved.yaml")
	require.NoError(t, os.Rename(path, renamed))
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{File: renamed}, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, "hotfix", got.Set)
	require.Equal(t, renamed, got.File)
}
