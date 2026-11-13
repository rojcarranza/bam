## bam preset

Run presets: list, show, add, rm, sync, migrate

```
bam preset [flags]
```

### Options

```
  -h, --help[=true|false]   help for preset
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

* [bam](bam.md)	 - A terminal remote control for Atlassian Bamboo
* [bam preset add](bam_preset_add.md)	 - Generate a run preset from a plan's declared variables
* [bam preset list](bam_preset_list.md)	 - List run presets from your presets files, .bam.yaml and the machine config
* [bam preset migrate](bam_preset_migrate.md)	 - Move presets from the machine config into your presets file
* [bam preset rm](bam_preset_rm.md)	 - Remove a run preset from every file that defines it
* [bam preset show](bam_preset_show.md)	 - Show a run preset with its defaults, options and origin
* [bam preset sync](bam_preset_sync.md)	 - Add new plan variables to a preset and mark removed ones

