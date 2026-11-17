# Cues

Cues are project-scoped quick actions that users invoke manually. Automatic workspace setup belongs in Project Settings workspace setup scripts.

## Command terminals

A command Cue opens a fresh interactive shell in the project or selected session's worktree. AO waits for that shell's startup hooks and first prompt before delivering the command. Project environment variables and startup-defined functions and aliases remain available. Agent Cues still deliver prompts to agents; command Cues do not.

For Bash, Zsh, POSIX sh, Fish, and Windows PowerShell, AO creates a private command file under its data directory only after readiness. The shell removes the file before executing the command, and AO bounds the consumption wait and removes pending files on errors, timeouts, or cancellation. The command is never passed in the shell's startup environment. Files use owner-only permissions on Unix and inherit a protected current-user/SYSTEM directory DACL on Windows. As with other same-user shell state, this does not isolate the command from other programs running as that user. An abrupt daemon crash between file creation and consumption can leave a private file under `cue-commands`; normal delivery and cancellation remove it.

The terminal prints the original command before executing it. Bash and Zsh also add it to interactive history, and PowerShell adds it to session history and PSReadLine when loaded. POSIX sh and Fish retain their native history behavior; copy the displayed command to run it again there rather than replaying the temporary-file dispatcher.

Windows Command Prompt (`cmd.exe`) keeps its existing direct PTY input transport, including its native quoting, line-editing, and command-length limitations. It does not use the command-file transport. Choose PowerShell or Git Bash for multiline scripts, heredocs, or literal tabs, using that shell's own syntax.
