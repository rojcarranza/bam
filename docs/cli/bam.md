## bam

A terminal remote control for Atlassian Bamboo

### Synopsis

bam triggers, watches and diagnoses Bamboo builds from the terminal.
Run bam alone on a terminal for the interactive UI.

```
bam [flags]
```

### Options

```
      --color string             color output: auto, always or never
      --debug[=true|false]       log HTTP requests to stderr
  -h, --help[=true|false]        help for bam
      --json[=true|false]        print JSON
      --no-tui[=true|false]      never open the terminal UI
      --project string           project the UI opens on (all: every project; default: default_project)
      --read-only[=true|false]   refuse every command that changes something in Bamboo (also BAM_READ_ONLY=1 or read_only: true in the config)
      --server string            server alias to use
  -v, --version[=true|false]     version for bam
```

### SEE ALSO

* [bam agent](bam_agent.md)	 - Build agents: list, show, mine, disable, enable
* [bam build](bam_build.md)	 - Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel
* [bam cancel](bam_cancel.md)	 - Stop a queued or running build
* [bam completion](bam_completion.md)	 - Generate the autocompletion script for the specified shell
* [bam doctor](bam_doctor.md)	 - Check config, credentials and what this Bamboo server supports
* [bam init](bam_init.md)	 - Write .bam.yaml for this repository, with generated presets
* [bam investigate](bam_investigate.md)	 - Find the error lines of a failed build and its likely cause
* [bam login](bam_login.md)	 - Store a verified personal access token for a server
* [bam logout](bam_logout.md)	 - Delete the stored token for a server
* [bam logs](bam_logs.md)	 - Print job logs; --failed for failed jobs only
* [bam open](bam_open.md)	 - Open a project, plan, build or job in the browser
* [bam pin](bam_pin.md)	 - Pins: add, rm, list
* [bam plan](bam_plan.md)	 - Plans: list, show, vars, branches, stats
* [bam preset](bam_preset.md)	 - Run presets: list, show, add, rm, sync, migrate
* [bam project](bam_project.md)	 - Projects: list
* [bam queue](bam_queue.md)	 - List the builds waiting for an agent, first in line first
* [bam run](bam_run.md)	 - Trigger a plan or preset with variables
* [bam server](bam_server.md)	 - Server aliases: add, list, rm, default
* [bam set](bam_set.md)	 - Release trains: presets run in ordered stages
* [bam skill](bam_skill.md)	 - Print the skill that teaches AI agents to use bam, or install it
* [bam status](bam_status.md)	 - Show every build you started with bam in the last day, and how it is going
* [bam top](bam_top.md)	 - Print what needs you: agents, the queue and your pinned pipelines
* [bam tree](bam_tree.md)	 - Show a build and every build it triggered, as a tree
* [bam ui](bam_ui.md)	 - Open the terminal UI
* [bam url](bam_url.md)	 - Print the Bamboo URL of a project, plan, build or job
* [bam usage](bam_usage.md)	 - Show how often you ran each command, or turn counting off or on
* [bam version](bam_version.md)	 - Print the bam version
* [bam watch](bam_watch.md)	 - Follow a build and the builds it triggers; exit 1 if any fails
* [bam whoami](bam_whoami.md)	 - Show the authenticated user on each configured server

