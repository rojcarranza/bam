# demo: in-repo
# demo: config
Output gifs/agent.gif
Output text/agent.txt
Set Height 520
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam agent list"
Enter
Sleep 2.5s
Type "bam agent mine add lab-agent"
Enter
Sleep 2s
Type "bam agent mine list"
Enter
Sleep 2s
Type "bam --read-only agent disable lab-agent-1 --yes"
Enter
Sleep 4s
