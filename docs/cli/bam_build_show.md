## bam build show

Show a build: state, stages, jobs, failure summary

```
bam build show [<build>|<plan>|<preset>] [flags]
```

### Examples

```
  bam build show PROJ-BUILD-482
  bam build show --last
  bam build show deploy-qa --branch develop
```

### Options

```
      --branch string   plan branch when a plan or preset is given
  -h, --help            help for show
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

