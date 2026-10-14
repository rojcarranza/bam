## bam preset migrate

Move presets from the machine config into your presets file

### Synopsis

migrate moves the presets in your machine config (top-level targets and every repos entry)
into your presets file (~/bam/presets.yaml, or BAM_PRESETS), renaming defaults to variables
and keeping comments, so they work from every directory. A name already in the presets file,
or defined more than once in the machine config, stays where it is. .bam.yaml is never touched.

```
bam preset migrate [flags]
```

### Examples

```
  bam preset migrate --dry-run
  bam preset migrate
```

### Options

```
      --dry-run   show what would move and write nothing
  -h, --help      help for migrate
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

