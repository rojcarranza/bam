## bam set

Release trains: presets run in ordered stages

```
bam set [flags]
```

### Options

```
  -h, --help   help for set
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
* [bam set history](bam_set_history.md)	 - List past set runs of this repository, newest first
* [bam set list](bam_set_list.md)	 - List release train sets
* [bam set rerun](bam_set_rerun.md)	 - Retry the failed runs of a set run, then finish the train
* [bam set resume](bam_set_resume.md)	 - Continue an interrupted or timed-out set run
* [bam set run](bam_set_run.md)	 - Run a set: each stage starts when every build of the one before succeeded
* [bam set show](bam_set_show.md)	 - Show a set's stages and variables
* [bam set status](bam_set_status.md)	 - Show a set run and the current state of its builds

