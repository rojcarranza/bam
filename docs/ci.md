# Using bam in CI

Set two variables; no config file is needed:

    export BAM_URL=https://bamboo.example.com
    export BAM_TOKEN=...        # used only for BAM_URL

    bam run PROJ-DEPLOY --var env=staging --watch --timeout 30m

`bam` never prompts in CI. On a pipe it prints one timestamped line per state change and no colors. `--watch` follows the build and every build it triggers, then prints the tree with a `failed:` line and link for each build that did not succeed.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | success (for `run` without `--watch`: the build was queued) |
| 1 | the watched build, or a build it triggered, finished without success |
| 2 | usage error |
| 3 | configuration error |
| 4 | authentication error |
| 5 | Bamboo, network, or unexpected error |
| 6 | `--timeout` reached; the builds are still running |
| 130 | interrupted; the build is still running |

## In a Bamboo script task

    set -e
    bam run PROJ-INTEGRATION --var sha="${bamboo.planRepository.revision}" --watch --timeout 45m

Store the token as a secret plan variable whose name contains `password` or `secret` so Bamboo masks it, and export it as `BAM_TOKEN`.
