# demo: in-repo
# demo: config
Output gifs/logs.gif
Output text/logs.txt
Set Height 640
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam logs {{CI0_RED}} --failed -n 14"
Enter
Wait@30s
Sleep 4s

Type "bam logs {{CI_GOOD}} --job UNIT -n 6"
Enter
Wait@30s
Sleep 3s
