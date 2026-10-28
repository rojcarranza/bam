## bam plan list

List plans of the configured projects, grouped by project

### Synopsis

List plans of the configured projects, or of the projects given, grouped by
project. With --match, list only the plans whose key or name contains the
text, ignoring case.

```
bam plan list [PROJ...] [flags]
```

### Examples

```
  bam plan list
  bam plan list OPS LAB
  bam plan list --match deploy
```

### Options

```
  -h, --help           help for list
      --match string   only plans whose key or name contains this text, ignoring case
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

* [bam plan](bam_plan.md)	 - Plans: list, show, vars, branches, stats

