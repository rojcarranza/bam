# bam: reuse a build's revision (`--revision from`)

**Status:** Implemented | **Date:** 2026-10-05
**Product requirements:** [`docs/prd.md`](../../prd.md) "Then (v0.3)"
**Builds on:** [`2026-09-24-bam-v0.3-run-revision-design.md`](2026-09-24-bam-v0.3-run-revision-design.md) (this is its §11 "later work"), [`2026-09-12-bam-cli-mvp-design.md`](2026-09-12-bam-cli-mvp-design.md) §2.2 (`--from`) and §7 (JSON contract)

All hosts, keys, user names and revisions are placeholders.

## 1. Problem

`bam run --from 41` reuses the variables of build 41 but builds the newest commit. Rebuilding exactly what build 41 built (a rollback, or reproducing a failure) means copying its commit from `bam build show` into `--revision` by hand. In the terminal UI the run form is prefilled from a build's variables, and its Revision row always opens empty, with nothing to say which commit that build used.

`bam run --from 41 --revision from` builds build 41's commit with build 41's variables. The run form offers the build's commit on its Revision row and fills it with one key.

## 2. What Bamboo returns

The build result that `GetBuild` already reads (`GET result/{key}?expand=…,vcsRevisions`) carries a top-level `vcsRevisionKey`: the commit of the plan's default repository. That is the repository `customRevision` applies to (run revision spec §2). The recording `internal/provider/bamboo/testdata/recorded/result_revision.json` has it next to the `vcsRevisions` list:

```json
"vcsRevisions": { "vcsRevision": [ { "repositoryName": "lab", "vcsRevisionKey": "9b30b9a…" } ] },
"vcsRevisionKey": "9b30b9a…"
```

A build that never started (`result_revision_invalid.json`) has no `vcsRevisionKey`.

Consequences: no new endpoint and no capability. Reusing a revision costs one `GetBuild` of the `--from` build.

## 3. Command

```
bam run <plan|target> --from N|KEY|last --revision from [other run flags]
```

- `--revision from` is a keyword, not a commit. It takes the default-repository commit of the build that `--from` names. A literal revision called `from` cannot be passed; Bamboo builds commits, not a ref named `from`.
- Without `--from` it is a usage error (exit 2), `--revision from needs --from`, checked before any configuration load or network call, beside the existing `--revision ""` check.
- `--from` is resolved to a build key **once** (`app.Service.ResolveFrom`). That key is used both for the variables and for the revision, so `--from last` cannot name two different builds when a new build starts between two lookups.
- `--revision REV` with any other value is unchanged. `--from` without `--revision from` still reuses variables only and builds the newest commit.
- `--branch`, `--edit`, `--watch`, `--timeout`, `--dry-run` and `--json` combine as they do with `--revision REV`.
- When the `--from` build has no revision (it never started, or Bamboo reports none), the run stops with a usage error (exit 2) before any trigger: What `PROJ-PLAN-41 has no revision to reuse`, Try `pass a commit with --revision`.

## 4. Output

### 4.1 Text

`view.RunPlan` labels a reused revision with its build, as variables from `--from` are labelled `from #N`:

```
Plan     PROJ-PLAN
Revision 9b30b9a (from #41)
Vars     env=qa (from #41)
```

The short form is the first seven characters (`provider.Revision.Short`'s rule), as in `bam build show`. The full commit is what is sent to Bamboo. A revision given as `--revision REV` still prints exactly as typed, with no label.

The queued hint and the not-built line (run revision spec §4.1) print the same short form when the revision was reused:

```
  revision 9b30b9a is checked when the build starts: bam watch PROJ-PLAN-42
Bamboo could not build revision 9b30b9a
```

### 4.2 `--edit`

The edit buffer's header names the source:

```
# bam run PROJ-PLAN · default branch · revision 9b30b9a (from #41) · server lab
```

### 4.3 JSON

`RunDoc` gains one field. `revision` holds the full commit that was sent.

```json
{ "key": "PROJ-PLAN-42", "url": "…", "plan_key": "PROJ-PLAN", "branch": "",
  "revision": "9b30b9aba03a4f45881629b03531006b89c1e0e7", "revision_from": "PROJ-PLAN-41", "variables": {} }
```

`revision_from` is `""` unless the revision was reused. No existing field changes.

## 5. Architecture

### 5.1 `provider`

`Build` gains `Revision string`: the commit of the plan's default repository, empty when Bamboo reports none. `Revisions` is unchanged. `provider/fake` builds carry whatever a test sets.

### 5.2 `provider/bamboo`

`resultDTO` gains `VcsRevisionKey string \`json:"vcsRevisionKey"\``, and the build mapping copies it into `Build.Revision`. No request changes.

### 5.3 `app`

- `PlanRef` gains `RevisionFrom string`: the build key the revision was taken from, set by the caller next to `Revision`, never by `ResolvePlan`.
- `PlanRef.RevisionLabel() string` returns what text output prints: `Revision` as given, or for a reused revision the short form plus ` (from #N)`. `RunPlan`, the edit buffer header and the queued and not-built lines use it (the last two without the ` (from #N)` suffix, through `PlanRef.RevisionShort()`).
- `Service.BuildRevision(ctx, key) (string, error)` reads the build with `P.GetBuild` and returns `Build.Revision`, or the usage error of §3 when it is empty.
- `Service.Run` is unchanged: it sends `ref.Revision`, and the stop-on-ignored-revision path applies as before.

### 5.4 `view`

- `RunPlan`: prints `ref.RevisionLabel()`.
- `Queued`, `RevisionNotBuilt`: called with `ref.RevisionShort()`; signatures unchanged.
- `RunJSON`: fills `revision_from` from `ref.RevisionFrom`.

### 5.5 `cli`

In `newRunCmd`, after `ResolvePlan`:

1. If `--from` is set, `key, err := svc.ResolveFrom(ctx, ref, from)`, and the key replaces `from` for `VarBase` / `ResolveVars`.
2. If `--revision` is `from`: `ref.Revision, err = svc.BuildRevision(ctx, key)` and `ref.RevisionFrom = key`. Otherwise `ref.Revision = revision` as today.

The `--revision` help text gains: `, or "from" to reuse the commit of the --from build`.

## 6. Terminal UI

- `formLoadedMsg` gains `FromRevision string`. After `VarBase`, when `base.FromBuild` is set, `openFormCmd` calls `svc.BuildRevision(ctx, base.FromBuild)`. Any error leaves `FromRevision` empty and is not shown; the hint is a convenience, like the variable prefill.
- `formState` gains `fromRevision` and `fromBuild`. The Revision row, while empty and with a hint, shows `newest` (dim) with `#41 built 9b30b9a` in its note column, where it otherwise reads `commit to build`. Without a hint it reads as today.
- `f`, with the cursor on the Revision row and a hint present, sets `revision` to the full commit; elsewhere in the form it does nothing. `f` is the existing "follow" binding of the log screen: keys already act per screen, and `TestNoKeyIsBoundTwice` forbids a second binding on the same key. The status bar's key line starts with `f use #41's revision` under the same conditions, and the `?` help lists `f` under RUN as `use the build's revision (in the form, on the revision row)`.
- A filled row shows the short form, with `from #41` in the note column, while the value equals the hint; typing over or clearing it works as today. Reopening the form starts empty again.
- `ctrl-R` sets `ref.Revision` from the row and `ref.RevisionFrom` to the build key when the row equals the hint, so the run's output and errors read as on the command line.

## 7. Errors and exit codes

| Case | Exit | What / Try |
| --- | --- | --- |
| `--revision from` without `--from` | 2 | `--revision from needs --from` |
| `--from` build has no revision | 2 | `PROJ-PLAN-41 has no revision to reuse` / `pass a commit with --revision` |
| `--from` build not found or unreadable | as today for `--from` | unchanged |
| Bamboo ignored the revision | 5 | unchanged (run revision spec §7) |

## 8. Testing

Test first, per the plan. No test sleeps or touches the network.

- **`provider/bamboo`**: a new hand-written fixture `testdata/result_revision.json` maps `vcsRevisionKey` to `Build.Revision`; a result without it maps to `""`. `TestRecorded*` checks the hand-written shape against the recording, and that the recorded invalid-revision result has no revision.
- **`app`**: `BuildRevision` returns the commit; an empty revision is the usage error of §3; a `GetBuild` error passes through. `RevisionLabel` and `RevisionShort` for a typed and a reused revision.
- **`cli`** (harness): exit 2 without `--from`, with no backend call; `--from 41 --revision from --dry-run` text (`Revision 9b30b9a (from #41)`) and JSON (`revision`, `revision_from`); the trigger receives the full commit; `--from last` lists builds once; a build without a revision exits 2 and triggers nothing; `--edit` header; the queued hint uses the short form; `revision_from` is `""` without the keyword.
- **`view`**: explicit expected strings for the labelled line; golden files only where a renderer changed.
- **TUI**: the hint on the empty row; `f` fills it; the footer entry appears only with a hint and on the row; no hint after a failed read; `ctrl-R` passes `Revision` and `RevisionFrom`; reopening starts empty.
- **e2e** (`//go:build e2e`): `bam run PLAN --from last --revision from --dry-run --json` reports the same `revision` as `bam build show --json` of the build in `revision_from`.

## 9. Documentation

- `make docs` regenerates `docs/cli/`.
- README: one line under `--revision` for `--revision from`.
- The run revision spec's §11 points here for this item.

## 10. Acceptance

Against the local Bamboo:

1. `bam run PLAN --revision <older SHA> --watch`, then `bam run PLAN --from last --revision from --watch` builds the same commit; `bam build show` lists it under Revisions for both builds.
2. `bam run PLAN --revision from` exits 2 with `--revision from needs --from` and triggers nothing.
3. `--from` on a build that never started (`--revision 0000000` earlier) exits 2 with `has no revision to reuse`.
4. TUI: `R` on a build shows the hint, `f` then `ctrl-R` builds that commit.
5. `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs` clean.

## 11. Out of scope

- A revision per repository in plans with several repositories; bam reuses the default repository's commit only.
- A `revision` key in presets.
- Prefilling the run form's Revision row without a keystroke.
- `--revision from` without `--from` defaulting to `--from last`.
