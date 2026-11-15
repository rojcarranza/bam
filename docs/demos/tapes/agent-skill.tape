Output gifs/agent-skill.gif
Output text/agent-skill.txt
Set Height 640
Source tapes/_common.tape
Source tapes/_start.tape
Type "bam skill | head -24"
Enter
Wait@30s
Sleep 4s

Type "bam skill install --dir ~/.agents/skills"
Enter
Wait@30s
Sleep 2s

Type "bam run SETLIST-CI --read-only"
Enter
Wait@30s
Sleep 3s
