## bam plan show

Show a plan: URL, branches, variables, recent builds

```
bam plan show <plan|preset> [flags]
```

### Examples

```
  bam plan show PROJ-BUILD
  bam plan show PROJ-BUILD --branch develop
  bam plan show provision --json
```

### Options

```
  -b, --branch string       plan branch for the recent builds
  -h, --help[=true|false]   help for show
```

### Options inherited from parent commands

```
      --color string             color output: auto, always or never
      --debug[=true|false]       log HTTP requests to stderr
      --json[=true|false]        print JSON
      --no-tui[=true|false]      never open the terminal UI
      --read-only[=true|false]   refuse every command that changes something in Bamboo (also BAM_READ_ONLY=1 or read_only: true in the config)
      --server string            server alias to use
```

### SEE ALSO

* [bam plan](bam_plan.md)	 - Plans: list, show, vars, branches, stats

