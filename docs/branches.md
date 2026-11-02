# Build a commit or another branch

How `bam run --revision` and `bam run --repo-branch` work. The README shows them in use; this page explains what bam does underneath.

## Build one commit: `--revision`

    bam run BAMCI-BUILD --revision 41d06ce

`--revision` takes a full or short SHA and passes it to Bamboo as given; Bamboo applies it to the plan's default repository. A revision Bamboo cannot build ends as "not built", so add `--watch` to see it. If the server ignores the revision, bam stops the build it queued and exits 5.

`--revision from` reuses the commit of the `--from` build, together with its variables:

    bam run BAMCI-BUILD --from 2 --revision from

## Build another repository branch: `--repo-branch`

    bam run BAMCI-BUILD --repo-branch bam=fix/cancel-stale-tree-reads

`--repo-branch REPO=BRANCH` builds `BRANCH` of repository `REPO` on every plan of the run whose default repository it is: the plan you run and the child plans its last build triggered (plus your `children:` config). `PLAN:REPO=BRANCH` sets it for one plan only, so a child can build its own branch; repeat the flag for more. Name the repository as `bam build show` lists it, under Revisions.

Bamboo cannot change a plan's repository over its REST API. So bam runs each plan on a **plan branch** named after the branch (`fix/cancel-stale-tree-reads` gives `fix-cancel-stale-tree-reads`), creating the ones that are missing. bam never edits the plans themselves: nothing needs restoring, and the plan branches stay for the next run.

- `--dry-run` shows the plan branches bam would create or reuse, and changes nothing.
- Bamboo starts a child's plan branch only when the parent plan's dependencies are enabled for branches, in the parent plan's configuration.
- A plan branch keeps the repository branch it was created with. To point it at another branch, delete the plan branch on its plan's Branches page in Bamboo, or with `DELETE /rest/api/latest/plan/<branch key>`; bam then creates it again.
- To run an existing plan branch without creating anything, use `bam run PLAN --branch NAME`.

## Example: check before you run

Test a pushed branch on the parent and both children, with `BAMCI-TEST` on its own branch. Check first with `--dry-run`:

    $ bam run BAMCI-BUILD --repo-branch bam=fix/cancel-stale-tree-reads \
        --repo-branch BAMCI-TEST:bam=fix/tree-preview-lazy-read --dry-run
    finding the plans BAMCI-BUILD triggers, for their repository branches
    Plan     BAMCI-BUILD  (branch fix-cancel-stale-tree-reads)
    Branches fix-cancel-stale-tree-reads on 3 plans
      BAMCI-BUILD  bam  fix/cancel-stale-tree-reads  create
      BAMCI-LINT   bam  fix/cancel-stale-tree-reads  create
      BAMCI-TEST   bam  fix/tree-preview-lazy-read   create
    Vars     none
    dry run: nothing triggered

`bam` is the repository's name on these plans. Drop `--dry-run` and add `--watch` to run it.

A preset can hold the same pairs under `repo_branches`; see [Build other repository branches](presets.md#build-other-repository-branches).

## How bam finds the child plans

Bamboo's API does not list a plan's child plans, so bam finds a child by its reason, "Child of BAMCI-BUILD-2", in the last 25 builds of each plan in your projects and in the build's own project. A `children:` map in the configuration narrows that and shows a child that never ran; see [Child plans](configuration.md#child-plans-children).
