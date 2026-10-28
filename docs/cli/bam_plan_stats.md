## bam plan stats

Success rate, durations and failing jobs over a plan's last builds

```
bam plan stats <plan|preset> [flags]
```

### Options

```
      --branch string   plan branch to read
  -h, --help            help for stats
  -n, --last int        how many of the newest builds to read (1 to 100) (default 20)
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

* [bam plan](bam_plan.md)	 - Plans: list, show, vars, branches, stats

