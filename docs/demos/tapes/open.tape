# demo: in-repo
# demo: config
Output gifs/open.gif
Output text/open.txt
Set Height 400
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam url SETLIST-DEPLOY"
Enter
Wait@30s
Sleep 1s

Type "bam url {{CI_GOOD}}"
Enter
Wait@30s
Sleep 1s

Type "bam open {{CI_GOOD}} --job UNIT"
Enter
Wait@30s
Sleep 3s
