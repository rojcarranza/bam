## bam pin add

Pin plans, plans with a branch, presets or plan names to a group of bam top

### Synopsis

Pin pipelines to a named group of bam top. Each PIN is a plan key (PROJ-PLAN), a
plan with a branch (PROJ-PLAN@release/1.2), a preset name, or the name of a plan.
A name is looked up on the selected server: a plan with exactly that name wins,
else the plans whose name holds all the words. bam stores the plan's key. A name
that fits no plan or several plans pins nothing, and none of the other pins are
written either. The pins go into the monitor: section of the machine file, which
keeps its comments.

```
bam pin add GROUP PIN... [flags]
```

### Examples

```
  bam pin add integration PROJ-BUILD
  bam pin add release PROJ-DEPLOY@release/1.2
  bam pin add integration "Integrate For Next Release" "Run Feature Tests"
```

### Options

```
  -h, --help[=true|false]   help for add
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

* [bam pin](bam_pin.md)	 - Pins: add, rm, list

