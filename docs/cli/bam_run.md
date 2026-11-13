## bam run

Trigger a plan or preset with variables

```
bam run <plan|preset> [flags]
```

### Examples

```
  bam run deploy-qa --watch
  bam run PROJ-BUILD --branch develop --var cluster_type=k8s
  bam run deploy-qa --from last --dry-run
  bam run PROJ-BUILD --from 482 --revision from
  bam run deploy-qa --repository repo-a=feature/PROJ-101
```

### Options

```
  -b, --branch string            plan branch to run; @ is the branch checked out here
      --dry-run                  resolve and validate variables, trigger nothing
      --edit                     review and change the variables in $EDITOR before running
      --from string              reuse variables of a build: number, key or last
  -h, --help                     help for run
      --notify                   when the watched build finishes, ring the terminal bell and raise a desktop notification where the system has one
      --repository stringArray   branch NAME=BRANCH a repository of the plan branch must build for this run, over the preset's repositories; BRANCH @ is the branch checked out here (repeatable)
      --revision string          commit to build instead of the newest, or "from" to reuse the commit of the --from build; Bamboo applies it to the plan's default repository
      --timeout duration         stop watching after this long (the build keeps running)
      --var stringArray          variable name=value (repeatable)
  -w, --watch                    follow the build and the builds it triggers until they finish
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

