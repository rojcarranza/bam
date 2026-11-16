# demo: in-repo
# demo: config
# demo: branch
Output gifs/watch-at.gif
Output text/watch-at.txt
Set Height 520
Set PlaybackSpeed 3
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam plan branches SETLIST-CI"
Enter
Wait@30s
Sleep 1.5s

Type "bam run ci --branch @ --watch=false"
Enter
Wait@20s
Sleep 1s

Type "bam watch @"
Enter
Wait@120s
Sleep 3s
