# demo: in-repo
# demo: config
# demo: idle
Output gifs/top.gif
Output text/top.txt
Set Height 720
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam pin add release SETLIST-CI SETLIST-DEPLOY SETLIST-SMOKE"
Enter
Sleep 1.5s
Type "bam top --once"
Enter
Sleep 4s
Type "bam top"
Enter
Sleep 4s
Type "q"
Sleep 1s
