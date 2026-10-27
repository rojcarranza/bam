## bam build cancel

Stop a queued or running build

### Synopsis

Stop a queued or running build.

Bamboo stops a build by removing its unfinished jobs from the queue, so a
stage that has not started yet can still be queued afterwards. Check the
result with bam build show.

```
bam build cancel [<build>|<plan>|<preset>] [flags]
```

### Options

```
      --branch string   plan branch when a plan or preset is given
  -h, --help            help for cancel
      --last            the last build bam triggered from this repository
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

* [bam build](bam_build.md)	 - Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel

