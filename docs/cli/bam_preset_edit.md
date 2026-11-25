## bam preset edit

Change a saved preset: values, branch, repositories, required, options, timeout or name

### Synopsis

Change a saved preset without opening its YAML. bam rewrites only the lines that change, in the
file whose copy of the preset bam uses, and keeps comments, key order and the other presets.
On a terminal, bam preset edit [NAME] opens a form filled with the saved preset: its variables,
the plan variables it leaves out (written only once you change them), then name, branch,
repositories, required and timeout. enter edits a row, ←→ step through the choices, x removes a
variable so the plan's own value applies, o edits a variable's options, ctrl-s saves and esc
writes nothing.
With --var, --unset, --branch, --timeout or --rename there is no form: bam applies them and
never asks, so scripts and agents can use it. --dry-run shows the changes and writes nothing.
A secret's value is never written: give ${ENV_NAME} and bam reads it from your shell at run
time. A preset that uses YAML anchors is refused, and so is a rename of a preset a set uses.

```
bam preset edit [name] [flags]
```

### Examples

```
  bam preset edit
  bam preset edit deploy-qa
  bam preset edit deploy-qa --var ENV=prod --unset REGION
  bam preset edit deploy-qa --branch release-2 --timeout 1h --dry-run
  bam preset edit deploy-qa --rename deploy-prod --json
```

### Options

```
  -b, --branch string          set the plan branch; "" removes it
      --dry-run[=true|false]   show the changes and write nothing
  -h, --help[=true|false]      help for edit
      --rename string          give the preset another name
      --timeout string         set the timeout, such as 45m; "" removes it
      --unset stringArray      remove a variable so the plan's own value applies (repeatable)
      --var stringArray        set a variable, name=value (repeatable)
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

