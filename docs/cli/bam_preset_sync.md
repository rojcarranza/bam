## bam preset sync

Add new plan variables to a preset and mark removed ones

### Synopsis

sync compares a preset with its plan's declared variables. New variables that preset add
would write (no plan default, or an empty secret) are added to the preset's variables (or
defaults, if the preset uses that older key) with the same values as preset add. Variables the plan no longer declares are kept and marked
"not declared on PLAN". Existing values are never changed.

```
bam preset sync <name> | --all [flags]
```

### Examples

```
  bam preset sync provision --dry-run
  bam preset sync provision
  bam preset sync --all
```

### Options

```
      --all[=true|false]       sync every preset
      --dry-run[=true|false]   show the changes and write nothing
  -h, --help[=true|false]      help for sync
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

* [bam preset](bam_preset.md)	 - Run presets: list, show, add, edit, rm, sync, migrate

