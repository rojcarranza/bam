## bam run

Trigger a plan or preset with variables

### Synopsis

Trigger a plan or preset with variables.

With no name, on a terminal, bam run opens a small picker of your presets: type to
filter on a name, plan, branch or file, Enter runs the highlighted one with the flags
you gave, Esc cancels. The picker does not edit variables; use --var or --edit.
Without a terminal, or with --json, a name is required.

With --plan, bam run opens the form of bam preset add on the plan's variables, with no
name to give and nothing saved. Name the plan, or on a terminal pick it and its branch
from a list; --from PROJ-BUILD-41, or that build's page link, names the plan and
opens the form on the build's values. Arrows step through the plan's value and recent
ones. ctrl-s prints one bam run line, copies it and starts nothing; ctrl-r starts the
build and prints the same line; Esc starts nothing. The line names the plan, carries
--branch, --revision, --repository and the values that differ from the plan, and leaves
out your server, --watch, --timeout and --notify. A secret is written as
"${NAME:?}", so whoever runs the line fills it from their shell.

```
bam run [plan|preset] [flags]
```

### Examples

```
  bam run deploy-qa --watch
  bam run PROJ-BUILD --branch develop --var cluster_type=k8s
  bam run deploy-qa --from last --dry-run
  bam run PROJ-BUILD --from 482 --revision from
  bam run deploy-qa --repository repo-a=feature/PROJ-101
  bam run                  # on a terminal: pick a preset from a list
  bam run --plan           # pick a plan, fill its variables in a form, print a line to share
  bam run PROJ-BUILD --plan
  bam run --plan --from PROJ-BUILD-41
```

### Options

```
  -b, --branch string            plan branch to run; @ is the branch checked out here
      --dry-run[=true|false]     resolve and validate variables, trigger nothing
      --edit[=true|false]        review and change the variables in $EDITOR before running
      --from string              reuse variables of a build: number, key or last
  -h, --help[=true|false]        help for run
      --notify[=true|false]      when the watched build finishes, ring the terminal bell and raise a desktop notification where the system has one
      --plan[=true|false]        open a form on the plan's variables, then print a bam run line to share (ctrl-s) or run it (ctrl-r)
      --repository stringArray   branch NAME=BRANCH a repository of the plan branch must build for this run, over the preset's repositories; BRANCH @ is the branch checked out here (repeatable)
      --revision string          commit to build instead of the newest, or "from" to reuse the commit of the --from build; Bamboo applies it to the plan's default repository
      --timeout duration         stop watching after this long (the build keeps running)
      --var stringArray          variable name=value (repeatable)
  -w, --watch[=true|false]       follow the build and the builds it triggers until they finish
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

