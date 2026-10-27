## bam build run

Trigger a plan or preset with variables

```
bam build run <plan|preset> [flags]
```

### Options

```
      --branch string      plan branch to run
      --dry-run            resolve and validate variables, trigger nothing
      --edit               review and change the variables in $EDITOR before running
      --from string        reuse variables of a build: number, key or last
  -h, --help               help for run
      --revision string    commit to build instead of the newest, or "from" to reuse the commit of the --from build; Bamboo applies it to the plan's default repository
      --timeout duration   stop watching after this long (the build keeps running)
      --var stringArray    variable name=value (repeatable)
      --watch              follow the build and the builds it triggers until they finish
```

### Options inherited from parent commands

```
      --color string    color output: auto, always or never
      --debug           log HTTP requests to stderr
      --json            print JSON
      --no-tui          never open the terminal UI
      --server string   server alias to use
```

### SEE ALSO

* [bam build](bam_build.md)	 - Builds: list, show, tree, run, rerun, watch, logs, cancel

