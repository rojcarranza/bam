# demo: in-repo
# demo: config
Output gifs/run-vars.gif
Output text/run-vars.txt
Set Height 520
Set PlaybackSpeed 3
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam run deploy --var version=1.0.1 --var environment=staging --var replicas=3"
Enter
Wait@120s
Sleep 4s
