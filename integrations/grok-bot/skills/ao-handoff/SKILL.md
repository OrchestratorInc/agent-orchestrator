---
name: ao-handoff
description: >-
  Hand an Orchestrator session's work to a Cursor cloud agent that returns a
  branch and a PR. Use for "send <session> to cursor", "hand off <session> to
  a cloud agent", "finish this in the cloud", "make a PR from <session> in the
  cloud".
---
Follow the ao-rules skill. Read-only on the user's computers. Launch only after the user confirms the brief.

## 1. Gather
1. Find the session (`<host>/<id>`). Read its last 20 turns (`aoapi GET "sessions/<id>/history?offset=0&limit=20"`) and its PRs (`aoapi GET "sessions/<id>/pr"`).
2. `aoapi GET "projects/<project>"` gives `repo` and `defaultBranch`. The session record has its `branch`.
3. The cloud agent sees only what is pushed. Check through Orchestrator: `aoapi GET "sessions/<id>"` has `branchState`, and `aoapi GET "sessions/<id>/workspace/manifest"` lists uncommitted files, on local and remote hosts alike. A host that can't answer the manifest: ask the user to confirm the session has no uncommitted changes. Files in the `uncommitted` section, no `branchState.remoteBranch` (never pushed) or `branchState.unpushed` above 0 (commits after the last push): ask the user to have the session commit and push first (ao-orchestrator). Start from an older base (the pushed branch as it is, or the default branch) only on the user's explicit yes, saying what it leaves out.
4. No GitHub repo: stop and say a cloud agent needs one.

## 2. Brief
```
Repo: <owner/name>   Base branch: <the session's pushed branch, or the default branch>
Goal: <one or two sentences from the whole session>
Context: <decisions, constraints, files and areas, conventions from AGENTS.md or CLAUDE.md>
Current state: <what is done, the open PR if any>
Task: <the next concrete steps>
Done when: <checks that must pass>
Deliver: a new branch and a PR against <base>. Do not merge.
```
Leave out keys, tokens, personal paths and personal data.

## 3. Launch
1. Show the brief and ask "Send this to Cursor?".
2. On yes, start a Cursor cloud agent (CloudAgent) on the full GitHub URL and the base branch. Source control not connected: ask the user to connect GitHub.
3. Report the agent's link. Tell the session's orchestrator, in one line, that the work moved to a cloud agent, so it doesn't continue in parallel (ao-orchestrator). When the cloud agent is done, report its branch, its PR and a three-line summary. Never merge.
