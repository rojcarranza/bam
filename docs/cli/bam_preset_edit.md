## bam preset edit

Change a saved preset: values, branch, repositories, required, options, timeout or name

### Synopsis

Change a saved preset without opening its YAML. bam edits the file whose copy of the preset
bam uses and keeps its comments, key order and the other presets; the spacing before a comment can change.
On a terminal, bam preset edit [NAME] opens a form filled with the saved preset: its variables,
the plan variables it leaves out (written only once you change them), then name, branch,
repositories, required and timeout. enter edits a row, ←→ step through the choices, x removes a
variable or repository (a variable then takes the plan's own value), o edits a variable's options,
a adds a repository, ctrl-s saves and esc writes nothing. When you change the branch, the form
reads the repositories Bamboo lists for it and offers them in place of the old ones.
With --var, --unset, --branch, --repository, --required, --option, --timeout or --rename there is
no form: bam applies them and never asks, so scripts and agents can use it, and it reads nothing
from Bamboo. --dry-run shows the changes and writes nothing.
A rename also renames the preset in the stages of the sets and the bam top pins in your project
and machine files, and in each train file a past bam set run --file used (a set in another
repository's own .bam.yaml keeps the old name, and so do the runs and sets of a repository whose
own .bam.yaml or machine entry defines a preset of that name). A rename is refused while a set or
pin in your machine file's top level, which applies in every repository, names a preset only
this repository defines.
A secret's value is never written: give ${ENV_NAME} and bam reads it from your shell at run
time. A preset that uses YAML anchors is refused.

```
bam preset edit [name] [flags]
```

### Examples

```
  bam preset edit
  bam preset edit deploy-qa
  bam preset edit deploy-qa --var ENV=prod --unset REGION
  bam preset edit deploy-qa --branch release-2 --timeout 1h --dry-run
  bam preset edit deploy-qa --repository app=release-2 --repository docs= --required ENV,REGION
  bam preset edit deploy-qa --option ENV=qa,prod
  bam preset edit deploy-qa --rename deploy-prod --json
```

### Options

```
  -b, --branch string            set the plan branch; "" removes it
      --dry-run[=true|false]     show the changes and write nothing
  -h, --help[=true|false]        help for edit
      --option stringArray       set a variable's allowed values, name=a,b; name= removes them (repeatable)
      --rename string            give the preset another name, and in the sets and pins that name it
      --repository stringArray   set a repository's branch, name=branch; name= removes it (repeatable)
      --required string          variables a run must have, separated by commas; "" removes them
      --timeout string           set the timeout, such as 45m; "" removes it
      --unset stringArray        remove a variable so the plan's own value applies (repeatable)
      --var stringArray          set a variable, name=value (repeatable)
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

* [bam preset](bam_preset.md)	 - Run presets: list, show, add, edit, rm, sync, migrate

