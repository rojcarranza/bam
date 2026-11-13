## bam build show

Show a build: state, stages, jobs, failure summary

```
bam build show [<build>|<plan>|<preset>] [flags]
```

### Examples

```
  bam build show PROJ-BUILD-482
  bam build show --last
  bam build show deploy-qa --branch develop
```

### Options

```
  -b, --branch string       plan branch when a plan or preset is given
  -h, --help[=true|false]   help for show
      --last[=true|false]   the last build bam triggered from this repository (the default for commands that only read)
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

