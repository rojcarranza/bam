# bam: a repository's branch for a run and its child plans (`bam run --repo-branch`)

**Status:** Implemented (terminal UI run form: §12) | **Date:** 2026-11-02
**Issue:** #130
**Builds on:** [`2026-09-12-bam-cli-mvp-design.md`](2026-09-12-bam-cli-mvp-design.md) (§7 JSON contract), [`2026-09-24-bam-v0.3-run-revision-design.md`](2026-09-24-bam-v0.3-run-revision-design.md) (per-run choices on `PlanRef`), [`2026-10-08-bam-build-tree-design.md`](2026-10-08-bam-build-tree-design.md) (finding a build's children)

All hosts, keys, user names, repositories and branches are placeholders.

## 1. Problem

A change spans a parent plan and the child plans it triggers. The parent builds `repo-a`, and so does a child. To test feature branches end to end, a developer today edits the repository branch of each plan in Bamboo by hand, and that edit changes the plan for everyone.

`bam run PROJ-PARENT --repo-branch repo-a=feature/PROJ-101` runs the parent on `feature/PROJ-101` of `repo-a`, and every child plan that builds `repo-a` builds the same branch, or a branch named for that child.

## 2. What Bamboo offers

From the Bamboo 9.6 REST reference (`swagger.json` published at `docs.atlassian.com/atlassian-bamboo/REST/9.6.0/`):

| Need | REST | Consequence |
| --- | --- | --- |
| Change a plan's repository branch | none; no resource edits a plan's repositories | bam cannot change a plan |
| Build another branch for one run | `PUT plan/{key}/branch/{name}?vcsBranch=V&enabled=true` creates a plan branch that builds `V` of the plan's **default** repository | bam creates plan branches; only the default repository can be switched |
| List a plan's branches | `GET plan/{key}/branch` (already used by `--branch`) | bam reuses a plan branch that already has the name |
| A plan's repositories | not on the plan resource; a build result's `vcsRevisions` names each repository, and `vcsRevisionKey` is the default repository's commit | bam learns a plan's repositories from its newest build |
| Read or change an existing plan branch's VCS branch | none | a reused plan branch keeps whatever branch it builds |
| Delete a plan branch | `DELETE plan/{branch key}` answers 204 (seen on Bamboo 12.1.8) | the run hint names it; bam does not delete plan branches itself |
| Pick a revision | the queue's `customRevision` (run revision spec) | default repository only, as here |

Bamboo's knowledge base ("How to trigger a branch after successful build of another branch with a different name") says a parent's plan branch triggers the child plan's branch **with the same name**, when the parent's dependencies are enabled for branches. That is how the branch reaches the children: bam gives every plan of the run a plan branch with one shared name.

Confirmed on the forge-lab parent/child pipeline (Bamboo 12.1.8, §10): `vcsBranch` takes a plain branch name such as `feature/x`; the parent's plan branch build starts the children's plan branches of the same name; and `PUT …/branch/{name}` for a name that exists answers 400 `This name is already used in a branch or plan.` bam lists the branches first, so it never sends that request.

## 3. Decisions

| Question (issue #130) | Decision | Why |
| --- | --- | --- |
| How the branch is changed | A plan branch per plan, all with one name; plans are never edited | The only REST route; nothing to restore and no effect on other people's builds |
| Temporary or persistent | The plan branches stay after the run and are reused by the next run with the same name | Deleting them would throw away their build history, and the next run would create them again; the plans themselves never change. bam says so, names the branches it created and how to delete one |
| How a repository is named | By its name as a build shows it (`vcsRevisions[].repositoryName`), such as `repo-a` | The name the developer sees in Bamboo and in `bam build show` |
| Which children | The plans in the tree of the root plan's newest build (`bam build tree`), the `children:` config, and any plan named in an override | Works with no setup; config and explicit names cover a child that never ran |
| `bam run` only, or the terminal UI too | `bam run` and presets first; the terminal UI run form in §12 | The UI refused a preset with overrides instead of ignoring them (§5.3) until §12 |

## 4. Command and presets

```
bam run <plan|preset> --repo-branch [PLAN:]REPO=BRANCH ... [--var k=v]... [--watch] [--dry-run] [--json]
```

- `--repo-branch` is repeatable. `REPO=BRANCH` applies to every plan of the run whose default repository is `REPO`. `PLAN:REPO=BRANCH` applies to that plan only and wins over `REPO=BRANCH` there.
- A value without `=`, an empty repository or branch, or a `PLAN` that is not a plan key is a usage error (exit 2), checked before any configuration load or network call.
- Combined with `--branch`, it is a usage error: bam chooses the plan branch. A preset that sets both `branch:` and `repo_branches` is a config error (exit 3).
- A preset holds the same entries under `repo_branches`, keyed like the flag:

  ```yaml
  presets:
    feature-101:
      plan: PROJ-PARENT
      repo_branches:
        repo-a: feature/PROJ-101
        PROJ-CHILD:repo-a: feature/PROJ-101-extra
  ```

  Layers merge per key, as `variables` do. A flag beats the preset entry with the same key. A key that does not parse is a config error (exit 3).

### 4.1 The plan branch name

All plans of the run use one plan branch name: the branch of the first override for every plan (`REPO=BRANCH`), or of the first override when every one names a plan, taking flags in the order given, then the preset's entries (those for every plan before those for one plan, each by key), with every `/` replaced by `-`. A flag for one plan on top of a preset so keeps the preset's name and plan branches, as older Bamboo versions name branches. `feature/PROJ-101` gives `feature-PROJ-101`. A name without `/` keeps Bamboo's path free of encoded slashes.

## 5. What a run does

### 5.1 Finding the plans

1. The root is the run's plan (`PlanRef.MasterKey`).
2. If the root has a build, bam reads the tree of its newest build (`BuildTree`, as `bam build tree` does) and keeps each plan in it with its parent plan and its node's build.
3. Each plan of the `children:` config reachable from the root, and each plan named in an override, joins the run; a named plan not found by 2 or the config hangs under the root.
4. A plan with no build from 2 gets its newest build read (`ListBuilds` limit 1, then `GetBuild`).

A plan's **default repository** is the `vcsRevisions` entry whose revision equals the build's `vcsRevisionKey`; its other entries are the plan's other repositories. A plan with no build has neither.

### 5.2 Checking the overrides (before anything is created or triggered)

| Case | Exit | What / Why / Try |
| --- | --- | --- |
| `REPO=B` and no plan of the run has `REPO` as its default repository, but one has it as another repository | 2 | `repo-a is not the default repository of PROJ-PARENT` / `Bamboo can switch only a plan's default repository; PROJ-PARENT's is repo-b` |
| `REPO=B` and no plan of the run builds `REPO` | 2 | `no plan of this run builds repo-a` / the default repository of each plan of the run |
| `PLAN:REPO=B` and `PLAN`'s default repository is another | 2 | as the first row, for that plan |
| `PLAN:REPO=B` and `PLAN` has no build | 2 | `PROJ-CHILD has no build, so bam cannot tell which repositories it builds` / `run it once, then try again` |
| An existing plan branch with the name is disabled | 2 | `plan branch feature-PROJ-101 of PROJ-CHILD is disabled` / `enable it in Bamboo: <its URL>` |

A plan **gets the plan branch** when it is the root, when an override sets its default repository's branch, or when it sits between the root and such a plan, so Bamboo's chain of same-named branches reaches it. A plan that only carries the name on builds its own branch (`vcsBranch` is left out).

For each plan that gets the plan branch, bam lists its branches. One whose short name equals the name is reused; otherwise bam will create it. Reused branches add one warning: `plan branch feature-PROJ-101 already exists on PROJ-PARENT12, PROJ-CHILD3; bam reuses it as it is and cannot check which branch it builds; to build another branch there, delete it in Bamboo first`.

### 5.3 Running

Unless `--dry-run`, bam creates the missing plan branches, deepest plans first and the root last, with `enabled=true` and `vcsBranch` when the plan's branch changes. A failed creation stops the run with the Bamboo error, and its Why names the branches already created. Then bam triggers the root's plan branch, as `bam run --branch` does.

`PlanRef` gains `RepoBranches []app.BranchPlan`: what the run does to each plan. `Service.Run` refuses a ref whose preset has `repo_branches` but whose `RepoBranches` is empty: `preset feature-101 sets repo_branches, which only bam run applies` / `bam run feature-101`. That covers a caller that does not apply them; the terminal UI's run form does (§12). A run set reports the same error while planning, as one of its problems, so no stage triggers.

`--watch` follows the tree as before, with each child's plan branch added to the plans searched for children (`TreeOptions.Plans`): the tree otherwise reads each plan's default branch only.

## 6. Output

### 6.1 Text

```
Plan     PROJ-PARENT  (branch feature-PROJ-101, PROJ-PARENT12)
Branches feature-PROJ-101 on 2 plans
  PROJ-PARENT  repo-a  feature/PROJ-101        reuse PROJ-PARENT12
  PROJ-CHILD   repo-a  feature/PROJ-101-extra  create
Vars     none
```

The Plan line names the root's plan branch key once it exists: `(branch feature-PROJ-101)` before bam creates it. A plan that only carries the name shows `its own branch` in the repository columns. After creating, one line per created branch, then the undo note, before the queued line:

```
✓ Created  PROJ-PARENT20   plan branch feature-PROJ-101 of PROJ-PARENT
✓ Created  PROJ-CHILD7      plan branch feature-PROJ-101 of PROJ-CHILD
  bam changed no plan, so nothing needs restoring; the plan branches stay for the next run.
  To remove one, delete it on its plan's Branches page in Bamboo, or with DELETE /rest/api/latest/plan/<branch key>.
  A child plan starts on feature-PROJ-101 only when its parent plan's dependencies are enabled for branches.
```

The dependency line appears when the run has a child plan.

### 6.2 JSON

`RunDoc` gains `repo_branches`, `[]` without overrides:

```json
"repo_branches": [
  {"plan_key": "PROJ-PARENT", "branch_key": "PROJ-PARENT12", "branch": "feature-PROJ-101",
   "repository": "repo-a", "vcs_branch": "feature/PROJ-101", "existing": true},
  {"plan_key": "PROJ-CHILD", "branch_key": "PROJ-CHILD7", "branch": "feature-PROJ-101",
   "repository": "repo-a", "vcs_branch": "feature/PROJ-101-extra", "existing": false}
]
```

`branch_key` is `""` on a dry run for a branch bam would create. `repository` and `vcs_branch` are `""` for a plan that only carries the name. No existing field changes; `branch` holds the plan branch name.

## 7. Architecture

- `provider`: `Provider.CreateBranch(ctx, planKey, name, vcsBranch) (Branch, error)`. `fake` records each call in `Created` and appends the branch to `Branches`; `CreateErrs` scripts failures by plan key.
- `provider/bamboo`: `CreateBranch` sends `PUT plan/{key}/branch/{name}` with `enabled=true` and `vcsBranch` when set; a 404 is the plan not found.
- `config`: `Target.RepoBranches StringMap` (`repo_branches`), merged like `Defaults`; `validateTarget` parses each key.
- `app`: `RepoBranch`, `ParseRepoBranch`, `RepoBranchOverrides(flags, target)`, `BranchPlan`, `Service.PlanRepoBranches`, `Service.CreateRepoBranches`, the `Service.Run` guard, `TreeOptions.Plans`.
- `view`: `RunPlan` prints the branch table; `RepoBranchesCreated` prints §6.1's lines; `RunJSON` fills `repo_branches`.
- `cli`: the `--repo-branch` flag and its checks.

## 8. Testing

No test sleeps or touches the network.

- `provider/bamboo` (`httptest`): the `PUT` path, `enabled=true`, `vcsBranch` present only when set; a 400 answer's message reaches the error.
- `config`: `repo_branches` loads, merges across layers, and a bad key is a config error.
- `app` (fake provider): parse rules; one name for all plans; a child found through the tree of the root's newest build and through `children:`; a per-plan override beating the general one; a plan between root and child carrying the name; each usage error of §5.2 with nothing created or triggered; reuse of an existing branch with its warning; creation order and the root triggered on its branch; `Run`'s preset guard; `TreeOptions.Plans` adding a candidate.
- `cli` (harness): `--dry-run` text and JSON; a real run creating the branches and triggering the root's branch; flag beating preset; `--repo-branch` with `--branch`; a malformed value exiting 2 with no backend call.
- `tools/bamboostub`: creating a plan branch, and a child of a branch build queued on the child's plan branch with the same name, so `bam run --repo-branch --watch` can be driven by hand.

## 9. Documentation

`make docs`; README run section; `docs/presets.md` (`repo_branches`); `docs/json.md` (`repo_branches`); CHANGELOG under Unreleased.

## 10. Acceptance

1. `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs` clean.
2. Against the stub (`-child PROJ-CHILD`, whose builds name their repository `app`), after one plain run of PROJ-PARENT: `bam run PROJ-PARENT --repo-branch app=feature/PROJ-101 --repo-branch PROJ-CHILD:app=feature/PROJ-101-extra --watch` creates both plan branches, runs the parent's, and the tree shows the child's branch build.
3. Against a forge-lab parent/child pipeline on a live Bamboo: the same command with its repository names, then `bam build show` of each build shows the chosen branch's commit of `repo-a`. This confirmed the three points of §2 that needed a live Bamboo.

## 11. Out of scope

- Switching a repository that is not a plan's default repository.
- Deleting plan branches, from `bam run` or the terminal UI; both name `DELETE plan/{branch key}` instead.
- Following child plan branches in `bam build tree` and `bam watch` of an existing build.

## 12. The terminal UI run form

**Issue:** BAM-23 (terminal UI piece 2). The form applies §4 to the same service calls `bam run` uses (`PlanRepoBranches`, `CreateRepoBranches`, `UseRepoBranches`, `Run`), so the checks of §5.2 and the plan branch name of §4.1 are the same.

- **Rows.** The form's rows are the variables, then the repository branches, then the revision. A preset's `repo_branches` entries arrive as rows keyed `REPO` or `PLAN:REPO` with the branch as the value. `b` adds a row on any plan's form: `REPO`, `PLAN:REPO` or `REPO=BRANCH`; a key already in the form moves the cursor to its row. The branch is typed or picked with `↑` `↓` from the repository's branches: `Provider.ListRepoBranches`, `GET plan/{key}/vcsBranches` (17 bam branches on the lab Bamboo 12.1.8), for the run's plan or the plan a `PLAN:REPO` row names. The plan's plan-branch names follow as extra suggestions. A server that answers 404 or 405 is `errs.ErrUnsupported` and leaves the plan branches, or typing. The hand-written fixture is `testdata/vcsBranches.json`. `recorded/vcsBranches.json` is that lab's answer, captured with `make record` (BAM-26): the wrapped `branches.branch` shape, and `TestRecordedVCSBranchesDecode` checks that every branch Bamboo counts in it is decoded. Only added rows are removed with `x`, as with variables. A row with no branch blocks `ctrl-R`.
- **Dry run.** `d` calls `PlanRepoBranches` only, so it reads Bamboo and writes nothing, and shows what §6.1 prints: `create` or `reuse <branch key>` for each plan, the reuse warning, and how to delete a plan branch. A refusal of §5.2 is shown in place of the list.
- **Run.** `ctrl-R` is the confirmation: it plans, creates the missing plan branches (deepest first), and triggers the root's plan branch, asking nothing more. The status bar reads `queued KEY on plan branch NAME (created N, reused M)` until the build leaves the queue, as the plain `queued KEY` does. A failure keeps the form open with the error, and the Why names the branches already created. Without repository rows, `ctrl-R` is unchanged.
- **Following.** The keys of the children's plan branches (`RepoPlan.ChildBranchKeys`) are kept for the build the run started and passed as `TreeOptions.Plans` to the plan screen's Tree panel and the tree screen, which then also search the child plans' branches. The scan shared with other builds of the plan is not used for such a tree.
- **Presets table.** The BRANCH column shows the plan branch, else the first repository branch as `REPO=BRANCH` with `+N` for more, else `default`.
- **Deleting.** The UI never deletes plan branches. It shows how in `d`.
- **Acceptance.** Form tests on the fake provider (`internal/view/tui/form_repo_test.go`); then on a live Bamboo: run the preset from the UI, run it again (the second run reuses all the plan branches), and delete the plan branches by hand. Done on the forge-lab Bamboo 12.1.8 (BAM-26): the first run created the plan branch on `BAMCI-BUILD`, `BAMCI-TEST` and `BAMCI-LINT`, the second reused all three, and all six builds passed.
