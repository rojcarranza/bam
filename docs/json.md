# JSON output

Every read command accepts `--json` and prints one JSON document to stdout. `watch --json` and `run --watch --json` print NDJSON: one event per line, ending with `{"type":"done","build":{...}}`.

Rules:

- every entity has `key` and `url`
- times are RFC3339 UTC strings or `null`
- durations are integers in `*_ms` fields
- lists are `[]`, never `null`
- secret values are `"********"`
- errors are plain text on stderr; the exit code gives the class

Within a major version fields are only added, never renamed, retyped or removed.

## Build

    {"key":"PROJ-BUILD-45","url":"...","plan_key":"PROJ-BUILD","plan_name":"Build and test","branch":"","number":45,"state":"failed",
     "reason":"Manual run by jdoe","custom_build":false,"labels":[],"queued_at":null,
     "started_at":"2026-09-12T11:56:20Z","finished_at":"2026-09-12T12:00:00Z","queue_duration_ms":0,
     "duration_ms":220000,"agent":"","revisions":[],"stages":[{"name":"Test","state":"failed",
     "duration_ms":31000,"jobs":[{"key":"PROJ-BUILD-INT-45","url":"...","name":"Integration",
     "state":"failed","duration_ms":31000}]}],"failed_tests":[]}

- `plan_name`: the plan's short name, as Bamboo shows it. For a branch build it is the master plan's name; `branch` holds the branch.

`state` is one of `queued`, `running`, `success`, `failed`, `stopped`, `skipped`, `not_built`, `unknown`.

A running build carries the server's duration estimate as well:

    "progress":{"percent":0.55,"average_ms":180000,"elapsed_ms":99000,"remaining_ms":81000,"stage":"Deploy","source":"bamboo"}

`percent` is a number between 0 and 1, `average_ms` the plan's average build duration, and `stage` the stage running now. The whole `progress` object is absent when the build is not running, or when no estimate can be had: Bamboo answers without a valid estimate, or it reports no progress and the plan branch's history gives none (fewer than 3 successful builds, or the history could not be read). `stage` is absent when the server does not name one. `source` says where the estimate comes from: `bamboo`, or `history` when the server reports no progress and bam estimates from the median of the plan branch's last 10 successful builds (at least 3).

## Plan

`plan list --json` and `plan show --json` give each plan an `enabled` boolean, and `plan branches --json` and the branches in `plan show --json` give one to each branch. It is `false` for a plan or branch that Bamboo has disabled; the text output marks those with `(disabled)`. When Bamboo leaves the flag out, `enabled` is `true`.

`plan show --json` includes a `"variables_error"` string field when the plan's variables could not be read (for example, when the Bamboo server does not expose the variables endpoint). The field is omitted entirely when reading the variables succeeds.

## Run

`run --json` without `--watch`, and `run --dry-run --json`, print one document:

    {"key":"PROJ-BUILD-46","url":"...","plan_key":"PROJ-BUILD","branch":"","revision":"9b30b9a...","revision_from":"PROJ-BUILD-41","variables":{"env":"qa"}}

`key` and `url` are `""` on a dry run. `revision` is the commit sent to Bamboo, `""` for the newest. `revision_from` is the build whose commit `--revision from` reused, `""` otherwise. `variables` holds the changed variables only, secrets masked.

## Cancel and rerun

`build cancel --json` prints `{"key":"PROJ-BUILD-46","url":"...","stopped":true}`. When the build had already finished, `stopped` is `false` and `state` holds its final state. With `--dry-run` it stops nothing and prints `{"key":"PROJ-BUILD-46","url":"...","state":"running","stopped":false}`, where `state` is the queued or running state of the build it would stop.

`build rerun --json` without `--watch` prints `{"key":"PROJ-BUILD-45","url":"...","restarted":true}`. With `--watch` it prints the watch events instead. With `--dry-run` it restarts nothing and prints `{"key":"PROJ-BUILD-45","url":"...","state":"failed","restarted":false}`.

## Plan statistics

`plan stats --json` prints one document over the plan's last builds (`--last`, default 20):

    {"plan_key":"PROJ-DEPLOY","branch":"","builds":20,"first":381,"last":400,
     "from":"2026-09-30T08:00:00Z","to":"2026-10-06T08:00:00Z",
     "passed":16,"failed":4,"other":{"stopped":1},"success_rate":0.8,
     "duration":{"median_ms":250000,"p90_ms":362000,"max_ms":580000,"max_build":388,"of":16},
     "streak":{"state":"success","count":3},
     "failing_jobs":[{"name":"Integration tests","failures":3,"last_build":397}],
     "warnings":[]}

Queued and running builds are not counted. `other` counts stopped, not built and skipped builds by state; `success_rate` is passed ÷ (passed + failed). `duration` is over passed builds only, and the median and p90 are real build durations. `success_rate`, `duration` and `streak` are `null` when there is nothing to compute them from, and `from`/`to` when no build was read. A failed build whose jobs could not be read counts under the job name `(unknown)` and adds a warning.

## Build tree

```json
{"state": "failed", "warnings": [], "root": {"plan_key": "PROJ-API", "status": "failed", "build": {...}, "children": [
  {"plan_key": "OPS-AUDIT", "status": "not_run", "build": null, "children": []}
]}}
```

- `state`: `success`, `failed` or `running`.
- `status`: the build's state, `not_run` for a configured child that never ran, or `loop` for a build already shown above (its `children` is empty). `superseded` marks an older run of the same child plan: its `build` and `children` are kept, and it does not count toward `state`.
- `from_log`: `true` for a build its parent's job log links to (a script started it through the REST API and printed its page), `false` for one whose reason names its parent. `stage` names the parent's stage whose job started it, and is present only with `from_log`.
- `build`: the `bam build show --json` document, without `progress`; `null` for `not_run`.
- `--watch --json` writes one such document per change, one per line.

## Build investigation

`build investigate --json` prints one document:

    {"build":{...},"likely_cause":"Error: quota exceeded (job TF)","jobs":[{"key":"PROJ-PROV12-TF-8","url":"...",
     "name":"Terraform","stage":"Apply","short":"TF","errors":[{"line":2,"text":"Error: quota exceeded",
     "pattern":"error","start":1,"context":["terraform apply","Error: quota exceeded"],"count":1}],"skipped_lines":0}]}

- `build`: the `bam build show --json` document, without `progress`.
- `jobs`: the failed jobs in stage order; `[]` when the build did not fail. A job whose log matched no pattern has `"errors":[]`.
- `likely_cause`: the first error of the first job that has one, `""` when none matched.
- `line` and `start` are 1-based log line numbers; `context` starts at `start` and holds the error line with `--context` lines either side. `count` is how often the same error text appears in the log.
- `skipped_lines`: how many lines at the start of a very long log were not read (Bamboo returns only its last part); `0` when the whole log was read. `line` and `start` still count from the start of the whole log.
- `pattern` is one of `error`, `fatal`, `maven`, `gradle`, `go-test`, `npm`, `panic`, `traceback`, `exception`, `bamboo-task`.
- `changes`: `null` without `--compare`. With it, `{"since":{...},"revisions":[{"name":"app","was":"9f8e7d6...","now":"a1b2c3d..."}],"variables":[...]}`. `since` is the last successful build of the same plan or branch before this one (a build document, `null` when none). `revisions` are by repository name, `variables` by variable name, both sorted by name and listing only what differs; `was` or `now` is `""` for a value present in one build only. Secret values are `********`. `variables` is `null` when the server cannot read build variables back.

## Events

    {"type":"state","time":"...","build_key":"PROJ-BUILD-45","state":"running","progress":{"percent":0.55,"average_ms":180000,"elapsed_ms":99000,"remaining_ms":81000,"source":"bamboo"}}
    {"type":"stage","time":"...","build_key":"PROJ-BUILD-45","name":"Test","state":"running"}
    {"type":"job","time":"...","build_key":"PROJ-BUILD-45","name":"Integration","key":"PROJ-BUILD-INT-45","state":"failed"}
    {"type":"done","time":"...","build_key":"PROJ-BUILD-45","state":"failed","build":{...}}

`state`, `stage` and `job` events carry `progress` while an estimate is available, from Bamboo or from the plan's history, with its `source`. `done` and error events never do: a finished build has a duration, not an estimate.

## Setup checks

`whoami --json` prints one row per configured server, sorted by alias (only the `--server` one when given):

    [{"alias":"work","url":"https://bamboo.example.com","user":"jdoe","full_name":"J Doe","error":""}]

`error` is `""` when the server answered. Otherwise `user` and `full_name` are `""` and `error` holds the message with its hint, for example `no token for server "lab" (https://bamboo.lab.example) (try: bam login lab)`. The rows are printed either way; the exit code is that of the first server that failed.

`doctor --json` checks the selected server and prints `{"checks":[{"name":"config","status":"ok","detail":"..."},...]}` in the order the checks ran: `config`, `server`, `token`, `auth`, then one per capability probed. `status` is `ok`, `error`, `unsupported` or `skipped`; `detail` is the path, server, user or reason shown in the text output, and for an `error` the message with its hint. Checks that depend on a failed one are left out. The exit code is that of the first check that failed.

## Sets

`set run --json` and `set resume --json` print NDJSON, one set event per line:

    {"type":"stage_start","time":"...","set":"release","id":"release-20260929T101500Z","stage":0}
    {"type":"run_triggered","time":"...","set":"release","id":"...","stage":0,"target":"publish-specs","build":{...}}
    {"type":"run_event","time":"...","set":"release","id":"...","stage":0,"target":"publish-specs","build":{...},"event":{"type":"state",...}}
    {"type":"run_done","time":"...","set":"release","id":"...","stage":0,"target":"publish-specs","build":{...},"state":"success"}
    {"type":"stage_done","time":"...","set":"release","id":"...","stage":0,"state":"success"}
    {"type":"set_done","time":"...","set":"release","id":"...","stage":1,"state":"failed"}

`stage` counts from 0. `run` is the run's name (`deploy[env=qa]` for a matrix run; the preset otherwise), and `target` its preset. `rerun` is the run's restart count on a `run_triggered` that restarted a build (`bam set rerun --failed`), and 0 on every other event. `event` is the watch event object above, unchanged; progress-only watch events are left out. A run that could not trigger ends with `run_done` carrying `"state":"failed"` and an `error` object (`kind`, `what`, `why`, `try`). The last line is `set_done`, or `set_error` with an `error` when the set could not go on (a set timeout, or a record that could not be written). An interrupted set ends without either.

`set status --json` prints the record: `id`, `set`, `origin`, `repo`, `status` (`running`, `successful`, `failed`, `interrupted`, `timed_out`), `started_at`, `finished_at`, `var_names`, `file` (the train file, for a set run from one), and `stages[].runs[]` with `target`, `name`, `plan_key`, `build_key`, `state`, `reruns` and `error`. Where the build could be fetched, `state` is its live state and `build` is the build object.

`set history --json` prints an array of these records for this repository, newest first, with the recorded states and no `build` objects.

`set list --json` and `set show --json` print each set's `name`, `timeout_ms`, `defined_in` and `stages[][]` of `{target, vars, matrix, runs}` (`matrix` only on a run that has one, as configured; `runs` the run names it expands to); secret values are masked and `${ENV}` references are shown as written. `set run --dry-run --json` prints `name`, `origin`, `warnings` and `stages[][]` of `{target, name, plan_key, branch, variables}`, with the changed variables only, masked.
