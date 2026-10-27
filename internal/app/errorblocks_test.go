package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorBlocksPatterns(t *testing.T) {
	cases := map[string]string{
		"│ Error: creating S3 Bucket (bam-artifacts): BucketAlreadyExists":     "error",
		"Error: UPGRADE FAILED: another operation is in progress":              "error",
		"fatal: [web-01]: FAILED! => {\"msg\": \"unreachable\"}":               "fatal",
		"fatal: could not read Username for 'https://example.com'":             "fatal",
		"[ERROR] Failed to execute goal on project app":                        "maven",
		"FAILURE: Build failed with an exception.":                             "gradle",
		"--- FAIL: TestLogin (0.01s)":                                          "go-test",
		"npm ERR! code ELIFECYCLE":                                             "npm",
		"panic: runtime error: index out of range":                             "panic",
		"Traceback (most recent call last):":                                   "traceback",
		"java.lang.IllegalStateException: no agent":                            "exception",
		"ValueError: bad input":                                                "exception",
		"Failing task since return code of [terraform] was 1 while expected 0": "bamboo-task",
		"\x1b[31m│\x1b[0m \x1b[1m\x1b[31mError: \x1b[0mtimeout":                "error",
	}
	for line, want := range cases {
		got := ErrorBlocks([]string{line}, 0, 0)
		if assert.Len(t, got, 1, line) {
			assert.Equal(t, want, got[0].Pattern, line)
		}
	}
	for _, line := range []string{"Starting integration tests", "0 errors, 0 warnings", "error handling enabled", "ok  example.com/app 0.2s"} {
		assert.Empty(t, ErrorBlocks([]string{line}, 0, 0), line)
	}
}

func TestErrorBlocksContextDedupeAndLimit(t *testing.T) {
	lines := []string{
		"terraform apply -auto-approve tfplan", // 1
		"aws_s3_bucket.artifacts: Creating...", // 2
		"Error: BucketAlreadyExists",           // 3
		"",                                     // 4
		"Error: BucketAlreadyExists",           // 5
		"retrying",                             // 6
		"Failing task since return code of [terraform] was 1 while expected 0", // 7
	}
	got := ErrorBlocks(lines, 1, 0)
	assert.Equal(t, []ErrorBlock{
		{Line: 3, Text: "Error: BucketAlreadyExists", Pattern: "error", Start: 2, Context: lines[1:4], Count: 2},
		{Line: 7, Text: lines[6], Pattern: "bamboo-task", Start: 6, Context: lines[5:7], Count: 1},
	}, got)

	got = ErrorBlocks(lines, 0, 1)
	assert.Len(t, got, 1)
	assert.Equal(t, 2, got[0].Count, "a repeat still counts once the limit is reached")
}
