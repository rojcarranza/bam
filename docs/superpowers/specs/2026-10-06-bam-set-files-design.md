# bam: run a set from a file (`bam set run -f`)

**Status:** Implemented | **Date:** 2026-10-06
**Product requirements:** [`docs/prd.md`](../../prd.md) §7 "Future", "Run sets" (batch files)
**Builds on:** [`2026-09-29-bam-run-sets-core-design.md`](2026-09-29-bam-run-sets-core-design.md) (configuration §3, record §5, resume §5.3), [`2026-10-05-bam-set-matrix-design.md`](2026-10-05-bam-set-matrix-design.md), [`2026-10-05-bam-set-rerun-failed-design.md`](2026-10-05-bam-set-rerun-failed-design.md)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

A one-off train — a hotfix release, a migration run once — has to be added to `.bam.yaml` as a set before `bam set run` can run it, then removed again. That puts a throwaway definition into a shared file.

`bam set run -f train.yaml` runs a set defined in a file of its own. Everything a configured set has works the same: stages, matrices, set vars, `--var`, dry run, the record, `status`, `resume` and `rerun --failed`.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| File shape | One set, the shape of an entry under `sets:`, plus an optional `name:` | Nothing new to learn; a set moves between a file and `.bam.yaml` unchanged |
| Name | `name:`, else the file name without its extension | The record, lock and `status` need a name |
| Presets | From the configuration, as for a configured set | A train file orders presets; presets stay where they are reviewed |
| Literal secrets | Refused, as in the project file | A train file is shared and committed like `.bam.yaml` |
| Resume and rerun | Re-read the file from the path in the record | bam never stores variable values, and a record holds no set definition |
| Rejected | A copy of the definition in the record | It would hold set vars, which may be values bam must not store |
| Rejected | Several sets in one file | One file, one train; a name per file keeps records simple |

## 3. The file

```yaml
name: hotfix-2041            # optional; default: the file name, hotfix-2041.yaml → hotfix-2041
timeout: 1h                  # optional, as for a configured set
stages:
  - [publish-specs]
  - - target: deploy
      vars: {deploy_token: "${DEPLOY_TOKEN}"}
      matrix: [{env: qa}, {env: prod}]
```

- Top-level keys: `name`, `timeout`, `stages`. Any other key is a config error naming it, as unknown keys in `.bam.yaml` are.
- `stages` follows the configuration's set schema exactly (run sets design §3.1, matrix design §3.1).
- The name must match the set name pattern `^[a-z][a-z0-9_-]*$`. Without `name:`, the file name without its extension is used; when that does not match, the error says to add `name:`.
- The checks of a configured set apply (run sets design §3.3, matrix design §3.3): presets exist after the targets merge, run names are unique per stage, at most 50 runs per stage, one server, durations parse. Every error is a config error (exit 3) naming the file.
- A literal secret in the file's `vars` or matrix values is a config error, as in the project file: use an `${ENV}` reference.

## 4. Commands

```
bam set run -f FILE [--var k=v]... [--dry-run] [--force]
bam set resume [set|id] [--file FILE] [--var k=v]... [--force]
bam set rerun  [set|id] --failed [--file FILE] [--var k=v]... [--force]
```

- `set run` takes a set name or `-f FILE`; both, or neither, is a usage error (exit 2), checked before any network call.
- `--file` on `resume` and `rerun` points at the train file when it has moved since the run; without it, the path in the record is read.
- `set status [set|id]` finds a file set's records by its name or id, as for any set; it does not read the file.
- The server is chosen from the file set's first preset, as for a configured set.

## 5. Record

`SetRecord` gains `File string \`json:"file,omitempty"\``: the absolute path of the train file, empty for a configured set. `bam set status --json` shows it as `file`.

Resume and rerun of a record with a file:

1. Read the file at `--file`, else at the recorded path. A file that is gone is a config error: `the train file of set run <id> is gone: <path>`, Try `bam set resume <id> --file <new path>`.
2. The file's set name must equal the record's set: otherwise a config error, `<path> defines set <x>, not <y>`.
3. The existing shape check applies: a file whose stages no longer list the same runs is `set <name> has changed since run <id>`.
4. The rest is unchanged: var names given again, the lock, and the run.

A file set shares the records and lock of a configured set of the same name. That is deliberate: the name is the train's identity.

## 6. Architecture

- **`config`**: `(*Config).LoadSetFile(path string) (ResolvedSet, error)` reads, decodes (with unknown keys refused), names, checks the set's secrets as the project file's are checked, and validates it against the merged targets with the existing `validateSet`. `DefinedIn` is `[path]`.
- **`app`**:
  - `SetOptions` gains `File string`. `PlanSet` reads the set with `LoadSetFile` when `File` is set, else with `Cfg.Set(name)`; the name argument is ignored for a file.
  - `SetPlan` gains `File string`, which `RunSet` writes into the record.
  - `ResumeSet`/`RerunSet` (through `continueSet`) set `o.File` from `--file` or the record before `PlanSet`, and check the name as §5 says.
- **`cli`**: `-f`/`--file` on `set run` (`run [set]`, `cobra.MaximumNArgs(1)`), `--file` on `resume` and `rerun`; connecting picks the server from the file's first preset.
- **`view`**: `SetRecordDoc` gains `File string \`json:"file,omitempty"\``; `set status` text prints `file <path>` under the header when set.

## 7. Errors and exit codes

| Case | Exit |
| --- | --- |
| `set run` with both a name and `-f`, or neither | 2 |
| File unreadable, unknown key, bad name, schema or §3 check | 3 |
| A literal secret in the file | 3 |
| The recorded file is gone, or names another set | 3 |
| The file's stages changed since the run | 3 |
| Everything else | as `bam set run` |

## 8. Testing

Test first. No test sleeps or touches the network.

- **`config`**: a full file parses; the name from `name:` and from the file name; a bad file name without `name:`; an unknown top-level key; an unknown preset; a duplicate run name; a literal secret refused; `${ENV}` accepted.
- **`app`**: `PlanSet` with `File` plans the file's set and ignores the name; `RunSet` records `File`; resume re-reads the file and continues; resume with the file gone, with `--file` at a new path, with a file naming another set, and with changed stages.
- **`cli`** (harness): `set run -f` dry run, run and status (`file` in JSON); a name and `-f` together exit 2 with no network; `set resume` of a file set; `set rerun --failed --file`.

## 9. Documentation

- `docs/configuration.md` "Sets (release trains)": a `### Train files` subsection with the file shape, naming and the resume rule.
- README Release trains: one line for `bam set run -f`.
- `docs/json.md`: `file` on `set status --json`.
- `make docs` regenerates `docs/cli/`.
- The run sets design §12 points here for batch files.

## 10. Out of scope

- Several sets in one file; reading a file from standard input.
- File sets in `bam set list` and in the terminal UI's `:sets`.
- Defining presets inside a train file.
- A copy of the definition in the record.
