## bam watch

Follow a build and the builds it triggers; exit 1 if any fails

### Synopsis

Follow a build and the builds it triggers; exit 1 if any fails.

bam draws the build tree, as "bam build tree <build> --watch" does, until
every build in it has finished, then lists each build that did not succeed.
Interrupting or timing out never stops a build. --json follows the one
build and writes its events as NDJSON.

```
bam watch [<build>|<plan>|<preset>] [flags]
```

### Options

```
      --branch string      plan branch when a plan or preset is given
  -h, --help               help for watch
      --last               the last build bam triggered from this repository
      --timeout duration   stop watching after this long (the build keeps running)
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

* [bam](bam.md)	 - A terminal remote control for Atlassian Bamboo

