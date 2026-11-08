# Vision

`bam` exists so that a developer spends less manual effort on Bamboo pipelines, from triggering them while building features to building, testing and improving them, and gets insight into how to make them better.
It serves developers and DevOps engineers who run Bamboo Data Center plans with variables, often many at once, and it turns their intent ("build my branch", "test these pipelines", "why did it fail") into the right REST calls, readable output and stable exit codes.
It owns exactly one thing: the command line for working with Bamboo pipelines, from intent to the Bamboo link when the browser is needed.

## Many pipelines at once

DevOps work means testing many pipelines at the same time, so running and watching several is a core use, not an extra.
Presets, sets, matrix runs and build trees exist so that one command starts a group and one view shows all of it.
Monitoring happens in the terminal first, and a web dashboard follows (see Three faces).
A feature that only helps one pipeline at a time is weighed against whether it scales to a group.

## Bamboo stays the source of truth

`bam` never treats its own cache as authoritative.
Every entity it shows carries its Bamboo URL, and `bam open` is always one step away.
When a run depends on state that lives in Bamboo, such as the branch a repository builds, `bam` checks it and sends the person to the Bamboo page that changes it.
What a DevOps engineer does in Bamboo, `bam` is meant to do: create plans and plan branches, add or change tasks, attach test scripts, and disable a task to isolate a new one.
The capability is not capped by what `bam` does today.
A change to Bamboo is something the person asks for by name, never a side effect of running a build.
Bamboo features the server does not report are learned lazily and cached with an expiry, never assumed.

## Three faces, one core

The commands and the terminal UI call the same use cases, so they cannot disagree.
A web dashboard is planned as the third face, where pipelines are seen and arranged by drag and drop, to raise developer productivity and give insight into how pipelines behave and how to improve them.
Every face adds nothing the core lacks.
Presentation lives in the view layer; the core never prints and never reads stdin.
A second CI provider would be one new package behind the provider interface, and it is not planned.

## Scripts are users with a contract

Every command works without a prompt, or stops with a flag that supplies the answer and a link that explains it.
`--json` field names are public: fields are added, never renamed or removed within a major version.
Exit codes mean the same thing in every command, and golden tests hold both.
Output is the important line first, with detail behind a flag or a follow-up command the output prints.

## Nothing destructive without a yes

Interrupting `watch` never stops a build; only `bam build cancel` does.
Restarting builds on a shared Bamboo asks first, and `--yes` is required without a terminal.
A set run checks every run before the first trigger and cancels nothing when a stage fails.
Intent is resolved and validated before anything is sent: missing variables, bad options and unset references are usage errors.

## Secrets and the public repository

Secret values print as `********` everywhere, including JSON and `--debug`.
A token is keyed by its server origin and is never sent to another host.
Credentials live in the OS keychain, a named environment variable, or a 0600 file, and nowhere else.
This repository is public: code, fixtures, docs and commits use placeholder hosts, keys and users only.

## Scope

`bam` is not a REST wrapper that exposes every endpoint, and it is organised around what a person intends, not around Bamboo's endpoints.
Bamboo agents and the web dashboard are coming into scope.
It does not handle deployment projects, Bamboo Server, basic authentication or Bamboo before 9.x.
It does not run Terraform, Ansible, Helm or kubectl, and it does not read clusters; deploy pipelines already wait for their own rollouts.
Features come from a written user story and a design, and a rejected design stays on record with its reason.

A change aligns when it turns a Bamboo intent into one command or keystroke, works for a group of pipelines as well as one, keeps Bamboo as the source of truth, and preserves JSON fields and exit codes.
A change should be resisted when it needs a second system, a prompt in a script path, a stored credential outside the three places, a real hostname in the repo, or an action on Bamboo state the person did not ask for.
