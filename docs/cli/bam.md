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
      --color string    color output: auto, always or never
      --debug           log HTTP requests to stderr
  -h, --help            help for bam
      --json            print JSON
      --no-tui          never open the terminal UI
      --server string   server alias to use
```

### SEE ALSO

* [bam build](bam_build.md)	 - Builds: list, show, run, watch, logs, cancel
* [bam completion](bam_completion.md)	 - Generate the autocompletion script for the specified shell
* [bam doctor](bam_doctor.md)	 - Check config, credentials and what this Bamboo server supports
* [bam init](bam_init.md)	 - Write .bam.yaml for this repository, with generated presets
* [bam login](bam_login.md)	 - Store a verified personal access token for a server
* [bam logout](bam_logout.md)	 - Delete the stored token for a server
* [bam logs](bam_logs.md)	 - Print job logs; --failed for failed jobs only
* [bam open](bam_open.md)	 - Open a project, plan, build or job in the browser
* [bam plan](bam_plan.md)	 - Plans: list, show, variables, branches
* [bam project](bam_project.md)	 - Bamboo projects
* [bam run](bam_run.md)	 - Trigger a plan or target with variables
* [bam server](bam_server.md)	 - Manage Bamboo server aliases
* [bam set](bam_set.md)	 - Release trains: presets run in ordered stages
* [bam target](bam_target.md)	 - Run presets: list, show, add, sync
* [bam ui](bam_ui.md)	 - Open the terminal UI
* [bam url](bam_url.md)	 - Print the Bamboo URL of a project, plan, build or job
* [bam version](bam_version.md)	 - Print the bam version
* [bam watch](bam_watch.md)	 - Follow a build until it finishes; exit 1 if it fails
* [bam whoami](bam_whoami.md)	 - Show the authenticated user on each configured server

