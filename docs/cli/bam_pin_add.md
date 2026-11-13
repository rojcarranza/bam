## bam pin add

Pin a plan, a plan with a branch, or a preset to a group of bam top

### Synopsis

Pin a pipeline to a named group of bam top. PIN is a plan key (PROJ-PLAN), a plan
with a branch (PROJ-PLAN@release/1.2) or a preset name. The pin goes into the
monitor: section of the machine file, which keeps its comments.

```
bam pin add GROUP PIN [flags]
```

### Examples

```
  bam pin add integration PROJ-BUILD
  bam pin add release PROJ-DEPLOY@release/1.2
```

### Options

```
  -h, --help   help for add
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

* [bam pin](bam_pin.md)	 - Pins: add, rm, list

