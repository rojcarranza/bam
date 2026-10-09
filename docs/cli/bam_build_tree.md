## bam build tree

Show a build and every build it triggered, as a tree

### Synopsis

Show a build and every build it triggered, as a tree.

bam finds a child build by its reason, "Child of <parent>", in the last 25
builds of each plan of the configured projects and of the build's project.
A children: map in the configuration narrows a plan's children and shows a
configured child that never ran. Interrupting --watch never stops a build.

```
bam build tree [<build>|<plan>|<target>] [flags]
```

### Options

```
      --branch string   plan branch when a plan or target is given
      --depth int       levels of builds to show below the root (default: all)
      --failed          show only the branches that lead to a failure
  -h, --help            help for tree
      --last            the last build bam triggered from this repository
      --no-watch        with --rerun-failed, restart and exit without following
      --rerun-failed    restart the failed builds whose parents succeeded, then follow the tree
      --watch           follow the tree until every build has finished
      --yes             with --rerun-failed, do not ask first
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

* [bam build](bam_build.md)	 - Builds: list, show, run, watch, logs, cancel

