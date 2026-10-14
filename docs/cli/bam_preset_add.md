## bam preset add

Generate a run preset from a plan's declared variables

### Synopsis

add reads the plan's declared variables from Bamboo and writes a preset named <name>.
It writes your presets file (~/bam/presets.yaml, or BAM_PRESETS), whose presets work in every
directory; --shared writes the repository's .bam.yaml instead. Edit the values under variables,
then run it with bam run <name>.

```
bam preset add <name> --plan KEY [flags]
```

### Examples

```
  bam preset add deploy-qa --plan PROJ-DEPLOY
  bam preset add deploy-qa --plan PROJ-DEPLOY --branch main --from last
  bam preset add provision-lab --plan PROJ-PROV --shared
  bam run deploy-qa --dry-run
```

### Options

```
      --branch string   default branch for the preset
      --force           replace an existing preset
      --from string     build whose values fill the comments: number, key or last
  -h, --help            help for add
      --plan string     plan key to generate from
      --print           print the YAML and write nothing
      --shared          write to the repository's .bam.yaml instead of your presets file
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

