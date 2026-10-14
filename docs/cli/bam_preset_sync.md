## bam preset sync

Add new plan variables to a preset and mark removed ones

### Synopsis

sync compares a preset with its plan's declared variables. New variables are added to the
preset's variables (or defaults, if the preset uses that older key) with the same values and
comments as preset add. Variables the plan no longer declares are kept and marked
"not declared on PLAN". Existing values are never changed.

```
bam preset sync <name> | --all [flags]
```

### Options

```
      --all       sync every preset
      --dry-run   show the changes and write nothing
  -h, --help      help for sync
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

* [bam preset](bam_preset.md)	 - Run presets: list, show, add, sync

