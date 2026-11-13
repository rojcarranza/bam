## bam plan branches

List a plan's branches

### Synopsis

List a plan's branches. With text, list only the branches whose name or key
contains it, ignoring case.

```
bam plan branches <plan|preset> [text] [flags]
```

### Examples

```
  bam plan branches PROJ-BUILD
  bam plan branches PROJ-BUILD feature
```

### Options

```
  -h, --help[=true|false]   help for branches
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

