# bam set history - design

Status: implemented. Closes the "set-level history views" item of the run-sets roadmap ([core design](2026-09-29-bam-run-sets-core-design.md)).

## Problem

bam records every set run in `$XDG_STATE_HOME/bam/sets/` and keeps the newest 20 per set, but only the newest one is reachable: `bam set status` shows a single run, and older ids are found only by listing the directory.

## Command

    bam set history [set]

- Lists the set runs recorded for this repository, newest first; with a set name, only that set's runs.
- Columns: `RUN` (the record id, which `status`, `resume` and `rerun` accept), `STATUS`, `STARTED`, `DURATION` (finish minus start; `–` while running or after an interrupt that left no finish time) and `BUILDS` (`successful/total` runs, as recorded).
- No network call: it shows the states bam recorded. `bam set status <id>` fetches the live state of one run.
- A set name that no layer defines and that has no records is a usage error with the did-you-mean hint other set commands give. A known set that never ran, or no runs at all, prints a note and exits 0, like the other list commands.
- `--json` prints an array of the `set status --json` record shape, without `build` objects; empty is `[]`.

## Out of scope

- Fetching live build states for every row (one request per run, up to 20 per set).
- A history view in the terminal UI; its sets panel already shows the newest run.
