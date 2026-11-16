# demo: in-repo
# demo: config
Output gifs/history.gif
Output text/history.txt
Set Height 760
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam build list SETLIST-CI -n 6"
Enter
Wait@30s
Sleep 2s

Type "bam build show {{CI_GOOD}}"
Enter
Wait@30s
Sleep 3s

Type "bam plan stats SETLIST-IT"
Enter
Wait@30s
Sleep 4s
