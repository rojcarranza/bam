## bam build list

List recent builds of a plan

### Synopsis

List recent builds of a plan. With no plan and no --branch, list the plan of
the last build bam triggered from this repository.

```
bam build list [<plan|preset>] [flags]
```

### Examples

```
  bam build list PROJ-BUILD
  bam build list
  bam build list deploy-qa --branch develop
  bam build list PROJ-BUILD --state failed -n 5
```

### Options

```
  -b, --branch string       plan branch
  -h, --help[=true|false]   help for list
  -n, --limit int           number of builds (default 10)
      --state string        only builds in this state, e.g. failed
```

### Options inherited from parent commands

```
      --color string          color output: auto, always or never
      --debug[=true|false]    log HTTP requests to stderr
      --json[=true|false]     print JSON
      --no-tui[=true|false]   never open the terminal UI
      --server string         server alias to use
```

### SEE ALSO

* [bam build](bam_build.md)	 - Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel

