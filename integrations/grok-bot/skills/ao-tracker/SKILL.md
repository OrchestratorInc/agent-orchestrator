---
name: ao-tracker
description: >-
  A living tracker for one project or launch: goal, milestones, todo,
  decisions, open questions, risks and a log, kept up to date from
  Orchestrator sessions, PRs and git. Use for "track <project>", "help me
  launch <project>", "update the <project> tracker", "what's next for
  <project>", "how close is <project> to done".
---
Follow the ao-rules skill. Write only the tracker file, never project files.

The tracker is `~/.ao-grok/trackers/<project>.md` on the host's `via` computer, with these sections: Goal, Milestones (checkbox and target date), Todo (now, next, later), Decisions (dated), Open questions, Risks, Links, Log (dated one-liners).

## Create
1. Find the host and project (ao-orchestrator step 1). `aoapi GET "projects/<project>"` gives its `path` and `repo`.
2. Read, on a local host: the README, docs, plans and TODO files at `path`, `git -C <path> log --oneline -30`, tags. On any host: the project's sessions and PRs (ao-status), and ask its orchestrator for the goal and plan if it has one (ao-orchestrator).
3. Nothing states the goal or a date: ask.
4. Show the goal and the top 5 next steps. Write the file after the user confirms.

## Update
Read what happened since the last Log line: finished sessions and their last replies, merged and open PRs, new commits, and decisions the user made in this chat ("we decided X"). Tick milestones, add decisions and open questions, add a Log line. Tell the user what changed and the next 3 steps.

## Suggest
Next steps come from the gap between the goal and the current state. For a release, check signing, the installer, the README, the changelog, a landing page and a demo. Add bugs from session replies and open questions. Offer to start one: send it to the project's orchestrator (ao-orchestrator) or a template (ao-templates).

"What's next for <project>" with no tracker: offer to create one, or ask the orchestrator instead.
