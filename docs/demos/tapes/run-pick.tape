# demo: in-repo
# demo: config
Output gifs/run-pick.gif
Output text/run-pick.txt
Set Height 520
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam run --dry-run"
Enter
Sleep 1500ms
Type "dep"
Sleep 2s
Ctrl+U
Type "smoke"
Sleep 1500ms
Enter
Wait@60s
Sleep 4s
