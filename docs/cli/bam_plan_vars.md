## bam plan vars

List a plan's variables with the values a recent build used

### Synopsis

List a plan's variables with the values a recent build used. With text, list
only the variables whose name contains it, ignoring case.

```
bam plan vars <plan|preset> [text] [flags]
```

### Examples

```
  bam plan vars PROJ-BUILD
  bam plan vars PROJ-BUILD cluster
```

### Options

```
  -b, --branch string       plan branch whose builds supply last-used values
      --from string         build for last-used values: number, key or last
  -h, --help[=true|false]   help for vars
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

