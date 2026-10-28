## bam set run

Run a set: each stage starts when every build of the one before succeeded

### Synopsis

Run a set: each stage starts when every build of the one before succeeded.

A failed build ends the set once its stage has finished; no build is ever
stopped. Interrupting stops watching only; continue with bam set resume.

A set is a list of stages, each a list of presets, under sets: in .bam.yaml:

  sets:
    release:
      - [publish-specs]
      - [provision, deploy]

A train file (-f) holds one set with the same stages. The full format,
with per-run vars, matrix and timeout, is in docs/configuration.md.

```
bam set run [set] [-f FILE] [flags]
```

### Examples

```
  bam set run release
  bam set run release --dry-run
  bam set run release --var version=1.4.0
  bam set run -f train.yaml
```

### Options

```
      --dry-run           resolve and check every run, trigger nothing
  -f, --file string       run the set defined in this train file instead of a configured set
      --force             take over the lock of a set run whose process is gone
  -h, --help              help for run
      --var stringArray   variable name=value for every run that declares it (repeatable)
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

* [bam set](bam_set.md)	 - Release trains: presets run in ordered stages

