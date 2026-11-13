## bam completion bash

Generate the autocompletion script for bash

### Synopsis

Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source <(bam completion bash)

To load completions for every new session, execute once:

#### Linux:

	bam completion bash > /etc/bash_completion.d/bam

#### macOS:

	bam completion bash > $(brew --prefix)/etc/bash_completion.d/bam

You will need to start a new shell for this setup to take effect.


```
bam completion bash
```

### Options

```
  -h, --help[=true|false]              help for bash
      --no-descriptions[=true|false]   disable completion descriptions
```

### Options inherited from parent commands

```
      --color string          color output: auto, always or never
      --debug[=true|false]    log HTTP requests to stderr
      --json[=true|false]     print JSON
      --no-tui[=true|false]   never open the terminal UI
      --server string         server alias to use
```

### SEE ALSO

* [bam completion](bam_completion.md)	 - Generate the autocompletion script for the specified shell

