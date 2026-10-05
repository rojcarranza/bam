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

    {"key":"PROJ-BUILD-45","url":"...","plan_key":"PROJ-BUILD","branch":"","number":45,"state":"failed",
     "reason":"Manual run by jdoe","custom_build":false,"labels":[],"queued_at":null,
     "started_at":"2026-09-12T11:56:20Z","finished_at":"2026-09-12T12:00:00Z","queue_duration_ms":0,
     "duration_ms":220000,"agent":"","revisions":[],"stages":[{"name":"Test","state":"failed",
     "duration_ms":31000,"jobs":[{"key":"PROJ-BUILD-INT-45","url":"...","name":"Integration",
     "state":"failed","duration_ms":31000}]}],"failed_tests":[]}

`state` is one of `queued`, `running`, `success`, `failed`, `stopped`, `skipped`, `not_built`, `unknown`.

A running build carries the server's duration estimate as well:

    "progress":{"percent":0.55,"average_ms":180000,"elapsed_ms":99000,"remaining_ms":81000,"stage":"Deploy"}

`percent` is a number between 0 and 1, `average_ms` the plan's average build duration, and `stage` the stage running now. The whole `progress` object is absent when the build is not running, or when the Bamboo server does not report progress. `stage` is absent when the server does not name one.

## Plan

`plan show --json` includes a `"variables_error"` string field when the plan's variables could not be read (for example, when the Bamboo server does not expose the variables endpoint). The field is omitted entirely when reading the variables succeeds.

## Run

`run --json` without `--watch`, and `run --dry-run --json`, print one document:

    {"key":"PROJ-BUILD-46","url":"...","plan_key":"PROJ-BUILD","branch":"","revision":"9b30b9a...","revision_from":"PROJ-BUILD-41","variables":{"env":"qa"}}

`key` and `url` are `""` on a dry run. `revision` is the commit sent to Bamboo, `""` for the newest. `revision_from` is the build whose commit `--revision from` reused, `""` otherwise. `variables` holds the changed variables only, secrets masked.

## Events

    {"type":"state","time":"...","build_key":"PROJ-BUILD-45","state":"running","progress":{"percent":0.55,"average_ms":180000,"elapsed_ms":99000,"remaining_ms":81000}}
    {"type":"stage","time":"...","build_key":"PROJ-BUILD-45","name":"Test","state":"running"}
    {"type":"job","time":"...","build_key":"PROJ-BUILD-45","name":"Integration","key":"PROJ-BUILD-INT-45","state":"failed"}
    {"type":"done","time":"...","build_key":"PROJ-BUILD-45","state":"failed","build":{...}}

`state`, `stage` and `job` events carry `progress` while the server reports it. `done` and error events never do: a finished build has a duration, not an estimate.

## Sets

`set run --json` and `set resume --json` print NDJSON, one set event per line:

    {"type":"stage_start","time":"...","set":"release","id":"release-20260929T101500Z","stage":0}
    {"type":"run_triggered","time":"...","set":"release","id":"...","stage":0,"target":"publish-specs","build":{...}}
    {"type":"run_event","time":"...","set":"release","id":"...","stage":0,"target":"publish-specs","build":{...},"event":{"type":"state",...}}
    {"type":"run_done","time":"...","set":"release","id":"...","stage":0,"target":"publish-specs","build":{...},"state":"success"}
    {"type":"stage_done","time":"...","set":"release","id":"...","stage":0,"state":"success"}
    {"type":"set_done","time":"...","set":"release","id":"...","stage":1,"state":"failed"}

`stage` counts from 0. `run` is the run's name (`deploy[env=qa]` for a matrix run; the preset otherwise), and `target` its preset. `rerun` is the run's restart count on a `run_triggered` that restarted a build (`bam set rerun --failed`), and 0 on every other event. `event` is the watch event object above, unchanged; progress-only watch events are left out. A run that could not trigger ends with `run_done` carrying `"state":"failed"` and an `error` object (`kind`, `what`, `why`, `try`). The last line is `set_done`, or `set_error` with an `error` when the set could not go on (a set timeout, or a record that could not be written). An interrupted set ends without either.

`set status --json` prints the record: `id`, `set`, `origin`, `repo`, `status` (`running`, `successful`, `failed`, `interrupted`, `timed_out`), `started_at`, `finished_at`, `var_names`, and `stages[].runs[]` with `target`, `name`, `plan_key`, `build_key`, `state`, `reruns` and `error`. Where the build could be fetched, `state` is its live state and `build` is the build object.

`set list --json` and `set show --json` print each set's `name`, `timeout_ms`, `defined_in` and `stages[][]` of `{target, vars, matrix, runs}` (`matrix` only on a run that has one, as configured; `runs` the run names it expands to); secret values are masked and `${ENV}` references are shown as written. `set run --dry-run --json` prints `name`, `origin`, `warnings` and `stages[][]` of `{target, name, plan_key, branch, variables}`, with the changed variables only, masked.
