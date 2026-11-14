// Package skills holds the agent skill bam ships. bam skill prints or installs
// it, so a go install build carries it without the repository.
package skills

import _ "embed"

// Bam is the SKILL.md that teaches an AI agent to use bam.
//
//go:embed bam/SKILL.md
var Bam string
