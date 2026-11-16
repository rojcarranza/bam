# demo: in-repo
# demo: config
# demo: idle
Output gifs/queue.gif
Output text/queue.txt
Set Height 560
Set PlaybackSpeed 2
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam run load --watch=false | tee /tmp/load.out"
Enter
Wait@20s
Sleep 1s
Hide
Type "L=$(grep -o 'SETLIST-LOAD-[0-9]*' /tmp/load.out | head -1) && clear"
Enter
Show
Type "echo $L"
Enter
Wait

Type "bam run ci --watch=false"
Enter
Wait@20s
Sleep 1s

Type "bam queue | grep -E '^#|SETLIST'"
Enter
Wait@30s
Sleep 4s

Type "bam build cancel $L"
Enter
Wait@30s
Sleep 3s

Type "bam queue | grep -E '^#|SETLIST'; bam watch --last"
Enter
Wait@120s
Sleep 3s
