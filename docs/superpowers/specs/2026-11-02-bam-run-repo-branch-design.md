# bam: a repository's branch for a run and its child plans (`bam run --repo-branch`)

**Status:** Implemented | **Date:** 2026-11-02
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
| Pick a revision | the queue's `customRevision` (run revision spec) | default repository only, as here |

Bamboo's knowledge base ("How to trigger a branch after successful build of another branch with a different name") says a parent's plan branch triggers the child plan's branch **with the same name**, when the parent's dependencies are enabled for branches. That is how the branch reaches the children: bam gives every plan of the run a plan branch with one shared name.

Not yet confirmed against a live Bamboo: how Bamboo answers `PUT …/branch/{name}` for a name that exists, whether `vcsBranch` takes `feature/x` or `refs/heads/feature/x`, and the child trigger itself. The forge-lab parent/child pipeline is the check (§10).

## 3. Decisions

| Question (issue #130) | Decision | Why |
| --- | --- | --- |
| How the branch is changed | A plan branch per plan, all with one name; plans are never edited | The only REST route; nothing to restore and no effect on other people's builds |
| Temporary or persistent | The plan branches stay after the run and are reused by the next run with the same name | Bamboo has no REST call to delete a plan branch; the plans themselves never change. bam says so and names the branches it created |
| How a repository is named | By its name as a build shows it (`vcsRevisions[].repositoryName`), such as `repo-a` | The name the developer sees in Bamboo and in `bam build show` |
| Which children | The plans in the tree of the root plan's newest build (`bam build tree`), the `children:` config, and any plan named in an override | Works with no setup; config and explicit names cover a child that never ran |
| `bam run` only, or the terminal UI too | `bam run` and presets now; the terminal UI later | Smallest change; the UI refuses a preset with overrides instead of ignoring them (§5.3) |

## 4. Command and presets

```
bam run <plan|preset> --repo-branch [PLAN:]REPO=BRANCH ... [--var k=v]... [--watch] [--dry-run] [--json]
```

- `--repo-branch` is repeatable. `REPO=BRANCH` applies to every plan of the run whose default repository is `REPO`. `PLAN:REPO=BRANCH` applies to that plan only and wins over `REPO=BRANCH` there.
- A value without `=`, an empty repository or branch, or a `PLAN` that is not a plan key is a usage error (exit 2), checked before any configuration load or network call.
- Combined with `--branch` (or a preset's `branch:`), it is a usage error: bam chooses the plan branch.
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

All plans of the run use one plan branch name: the branch of the first override (flags in the order given, then the preset's entries: those for every plan before those for one plan, each by key), with every `/` replaced by `-`, as older Bamboo versions name branches. `feature/PROJ-101` gives `feature-PROJ-101`. A name without `/` keeps Bamboo's path free of encoded slashes.

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

For each plan that gets the plan branch, bam lists its branches. One whose short name equals the name is reused; otherwise bam will create it. A reused branch with an override adds a warning: `PROJ-CHILD: plan branch feature-PROJ-101 (PROJ-CHILD3) already exists; bam cannot check which branch of repo-a it builds`.

### 5.3 Running

Unless `--dry-run`, bam creates the missing plan branches, deepest plans first and the root last, with `enabled=true` and `vcsBranch` when the plan's branch changes. A failed creation stops the run with the Bamboo error, and its Why names the branches already created. Then bam triggers the root's plan branch, as `bam run --branch` does.

`PlanRef` gains `RepoBranches []app.BranchPlan`: what the run does to each plan. `Service.Run` refuses a ref whose preset has `repo_branches` but whose `RepoBranches` is empty: `preset feature-101 sets repo_branches, which only bam run applies` / `bam run feature-101`. That covers run sets and the terminal UI's run form, which would otherwise ignore the overrides.

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

A plan that only carries the name shows `its own branch` in the repository columns. After creating, one line per created branch, then the undo note, before the queued line:

```
✓ Created  PROJ-CHILD7   plan branch feature-PROJ-101 of PROJ-CHILD
  bam changed no plan, so there is nothing to restore; these plan branches stay for the next run.
  PROJ-CHILD only starts on feature-PROJ-101 when PROJ-PARENT's dependencies are enabled for branches.
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
2. Against the stub: `bam run PROJ-PARENT --repo-branch repo-a=feature/PROJ-101 --repo-branch PROJ-CHILD:repo-a=feature/PROJ-101-extra --watch` creates both plan branches, runs the parent's, and the tree shows the child's branch build.
3. Against a forge-lab parent/child pipeline on a live Bamboo: the same command, then `bam build show` of each build shows the chosen branch's commit of `repo-a`. This confirms the three unverified points of §2.

## 11. Out of scope

- The terminal UI run form.
- Switching a repository that is not a plan's default repository.
- Deleting plan branches (no REST call).
- Following child plan branches in `bam build tree` and `bam watch` of an existing build.
