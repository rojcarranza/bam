## bam build

Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel

```
bam build [flags]
```

### Options

```
  -h, --help[=true|false]   help for build
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

* [bam](bam.md)	 - A terminal remote control for Atlassian Bamboo
* [bam build cancel](bam_build_cancel.md)	 - Stop a queued or running build
* [bam build investigate](bam_build_investigate.md)	 - Find the error lines of a failed build and its likely cause
* [bam build list](bam_build_list.md)	 - List recent builds of a plan
* [bam build logs](bam_build_logs.md)	 - Print job logs; --failed for failed jobs only
* [bam build rerun](bam_build_rerun.md)	 - Rerun a failed build's failed jobs
* [bam build run](bam_build_run.md)	 - Trigger a plan or preset with variables
* [bam build show](bam_build_show.md)	 - Show a build: state, stages, jobs, failure summary
* [bam build tree](bam_build_tree.md)	 - Show a build and every build it triggered, as a tree
* [bam build watch](bam_build_watch.md)	 - Follow a build and the builds it triggers; exit 1 if any fails

