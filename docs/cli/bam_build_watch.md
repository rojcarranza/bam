## bam build watch

Follow a build and the builds it triggers; exit 1 if any fails

### Synopsis

Follow a build and the builds it triggers; exit 1 if any fails.

bam draws the build tree, as "bam build tree <build> --watch" does, until
every build in it has finished, then lists each build that did not succeed.
Interrupting or timing out never stops a build. --json follows the one
build and writes its events as NDJSON.

bam watch @ follows the build of the commit you have checked out: it waits
(up to --timeout, 10 minutes by default) until Bamboo starts one, then
follows it. The plan is the one you last ran in this repository; bam watch
<plan|preset> --branch @ names another. A commit that is not pushed has no
build, so bam says so and follows the newest build of the branch.

```
bam build watch [<build>|<plan>|<preset>] [flags]
```

### Examples

```
  bam watch PROJ-BUILD-482
  bam watch --last
  bam watch deploy-qa --branch develop
  bam watch PROJ-BUILD-482 --timeout 30m
  git push && bam watch @
```

### Options

```
      --branch string      plan branch when a plan or preset is given
  -h, --help               help for watch
      --last               the last build bam triggered from this repository
      --notify             when the watched build finishes, ring the terminal bell and raise a desktop notification where the system has one
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

* [bam build](bam_build.md)	 - Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel

