## bam plan

Plans: list, show, vars, branches, stats

```
bam plan [flags]
```

### Options

```
  -h, --help[=true|false]   help for plan
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
* [bam plan branches](bam_plan_branches.md)	 - List a plan's branches
* [bam plan list](bam_plan_list.md)	 - List plans of the configured projects, grouped by project
* [bam plan show](bam_plan_show.md)	 - Show a plan: URL, branches, variables, recent builds
* [bam plan stats](bam_plan_stats.md)	 - Success rate, durations, waiting for an agent, slowest jobs and failing jobs over a plan's last builds
* [bam plan vars](bam_plan_vars.md)	 - List a plan's variables with the values a recent build used

