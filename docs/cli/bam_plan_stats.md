## bam plan stats

Success rate, durations, waiting for an agent, slowest jobs and failing jobs over a plan's last builds

```
bam plan stats <plan|preset> [flags]
```

### Examples

```
  bam plan stats PROJ-BUILD
  bam plan stats PROJ-BUILD -n 50
  bam plan stats provision --branch develop
```

### Options

```
  -b, --branch string       plan branch to read
  -h, --help[=true|false]   help for stats
  -n, --last int            how many of the newest builds to read (1 to 100) (default 20)
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

* [bam plan](bam_plan.md)	 - Plans: list, show, vars, branches, stats

