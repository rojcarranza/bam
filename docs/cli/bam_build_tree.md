## bam build tree

Show a build and every build it triggered, as a tree

### Synopsis

Show a build and every build it triggered, as a tree.

bam finds a child build by its reason, "Child of <parent>", in the last 25
builds of each plan of the configured projects and of the build's project.
A children: map in the configuration narrows a plan's children and shows a
configured child that never ran. --timing adds a timeline of the finished
run: each build's wait for an agent and its run on one time axis.
Interrupting --watch never stops a build.
On a terminal, bam shows on stderr what it is reading while the tree loads.

```
bam build tree [<build>|<plan>|<preset>] [flags]
```

### Examples

```
  bam build tree PROJ-BUILD-482
  bam build tree --last --watch
  bam build tree provision --failed --depth 2
  bam build tree PROJ-BUILD-482 --rerun-failed --yes
  bam build tree PROJ-BUILD-482 --timing
```

### Options

```
  -b, --branch string               plan branch when a plan or preset is given
      --depth int                   levels of builds to show below the root (default: all)
      --failed[=true|false]         show only the branches that lead to a failure
  -h, --help[=true|false]           help for tree
      --last[=true|false]           the last build bam triggered from this repository (the default for commands that only read)
      --no-watch[=true|false]       with --rerun-failed, restart and exit without following
      --rerun-failed[=true|false]   restart the failed builds whose parents succeeded, then follow the tree
      --timing[=true|false]         after the tree, draw each build's wait and run on one time axis
  -w, --watch[=true|false]          follow the tree until every build has finished
      --yes[=true|false]            with --rerun-failed, do not ask first
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

* [bam build](bam_build.md)	 - Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel

