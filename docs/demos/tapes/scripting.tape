# demo: in-repo
# demo: config
Output gifs/scripting.gif
Output text/scripting.txt
Set Height 620
Set PlaybackSpeed 2
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam build list SETLIST-CI -n 3 --json | jq -r '.[] | [.key, .state] | @tsv'"
Enter
Wait@30s
Sleep 3s

Type "bam run ci --read-only"
Enter
Wait@30s
Sleep 3s

Type "bam build show {{CI_GOOD}} --json | jq '{key, state, duration_ms}'"
Enter
Wait@30s
Sleep 3s

Type "bam run ci --watch > /dev/null; echo exit code $?"
Enter
Wait@120s
Sleep 3s
