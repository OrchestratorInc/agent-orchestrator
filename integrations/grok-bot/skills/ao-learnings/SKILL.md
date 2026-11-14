---
name: ao-learnings
description: >-
  Save durable lessons from an Orchestrator session into the project's
  AGENTS.md or CLAUDE.md, so later agents start with them. Only when the user
  explicitly says "save learnings", "save what we learned", "add this to
  AGENTS.md", optionally "from <session>".
---
Follow the ao-rules skill. Writes one file, and only after a yes to the exact diff.

1. Find the session (`<host>/<id>`; none named: the project's orchestrator). Read its last 40 turns: `aoapi GET "sessions/<id>/history?offset=0&limit=40"`.
2. Keep only durable lessons: commands that work, gotchas, conventions, the user's decisions, mistakes agents repeated. Skip one-off details, keys, personal paths and anything the file already says.
3. Where it goes:
   - The project has a live orchestrator: send it the lessons and ask it to add them to the project's agent instructions in a PR (ao-orchestrator). It knows the repo's rules for that file. Stop here.
   - No orchestrator, local host: `aoapi GET "projects/<project>"` gives `path`. Target `AGENTS.md` if it exists there, else `CLAUDE.md`, else propose a new `AGENTS.md`.
   - No orchestrator, remote host: tell the user to start one, or to add the lessons themselves; show them.
4. Read the target, then show a unified diff of 5 to 12 bullets under `## Learnings` (or the closest section). Ask "Write this to <file>?".
5. On yes, apply exactly that diff, appending or inserting, never rewriting the file. Read it again to confirm. Say the change is uncommitted in the main checkout; don't commit or push unless asked.
