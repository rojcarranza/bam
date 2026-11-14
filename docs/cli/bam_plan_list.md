## bam plan list

List plans of the configured projects, grouped by project

### Synopsis

List plans of the configured projects, or of the projects given, grouped by
project. With --match, list only the plans whose key or name contains the
text, ignoring case. A project can also be a link copied from its Bamboo page.

```
bam plan list [PROJ...] [flags]
```

### Examples

```
  bam plan list
  bam plan list OPS LAB
  bam plan list https://bamboo.example.com/browse/OPS
  bam plan list --match deploy
```

### Options

```
  -h, --help[=true|false]   help for list
      --match string        only plans whose key or name contains this text, ignoring case
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

