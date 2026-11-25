## bam preset add

Generate a run preset from a plan's declared variables

### Synopsis

add reads the plan's declared variables from Bamboo and writes a preset named <name>.
It writes the plan's project file in your presets directory (~/bam/presets/proj.yaml for
PROJ-DEPLOY, or beside BAM_PRESETS), creating it if needed; those presets work in every
directory. --file NAME writes NAME.yaml in that directory instead, for presets that belong to no
one project; --shared writes the repository's .bam.yaml. Edit the values under variables,
then run it with bam run <name>. --from with a full build key implies --plan and the build's
branch. The preset lists every variable the plan declares, with its plan default filled in; a secret
the plan stores is a commented line. It also writes the server you connected to, the plan branch,
and watch: true, with no per-line comments except one on a variable the plan does not declare.
--from writes only the plan's declared variables; --include-globals adds the other variables
the build ran with, which Bamboo fills with every global.
On a terminal, add first opens a form with one row per variable: type a value, step through
the plan default and up to three values the last 10 builds ran with with the left and right
arrows, ctrl-s writes the file, esc writes nothing. With no name, or no --plan, it asks for
them first. With a pipe, --no-tui, --json or --print there is no form: add writes the plan's
values and prints a box, in the terminal only, with those recent values for each variable that varied.

```
bam preset add <name> --plan KEY [flags]
```

### Examples

```
  bam preset add deploy-qa --plan PROJ-DEPLOY
  bam preset add deploy-qa --plan PROJ-DEPLOY --branch main --from last
  bam preset add deploy-eu --from PROJ-DEPLOY-41
  bam preset add deploy-package --plan PROJ-PKG --file custom-presets
  bam preset add provision-lab --plan PROJ-PROV --shared
  bam run deploy-qa --dry-run
```

### Options

```
  -b, --branch string                  default branch for the preset; defaults to the --from build's branch
      --file string                    write NAME.yaml in your presets directory instead of the plan's project file
      --force[=true|false]             replace an existing preset
      --from string                    build whose variable values fill the preset: number, key or last
  -h, --help[=true|false]              help for add
      --include-globals[=true|false]   with --from, also write the build's variables the plan does not declare (Bamboo globals)
      --plan string                    plan key or plan page link to generate from; optional when --from is a build key
      --print[=true|false]             print the YAML and write nothing
      --shared[=true|false]            write to the repository's .bam.yaml instead of your presets directory
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

