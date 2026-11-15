Output gifs/usage.gif
Output text/usage.txt
Set Height 600
Source tapes/_common.tape
Source tapes/_start.tape
Type "bam plan list SETLIST > /dev/null"
Enter
Wait@30s
Sleep 0.5s

Type "bam build list SETLIST-CI -n 3 > /dev/null"
Enter
Wait@30s
Sleep 0.5s

Type "bam usage"
Enter
Wait@30s
Sleep 4s

Type "bam usage off"
Enter
Wait@30s
Sleep 1.5s
