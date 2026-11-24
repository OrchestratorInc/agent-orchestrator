---
name: ao-templates
description: >-
  Saved prompt templates with {vars} for starting Orchestrator work. Use for
  "run template <name>", "use my <name> template on <project>", "list
  templates", "save this as a template", "edit my <name> template".
---
Follow the ao-rules skill. Run only the template the user names.

Templates live in `~/.ao-grok/templates.md` on the main computer (memory: "Templates on <computer>"; first use: the computer the message came from). Each is one section:
````
## <name>
purpose: <one line>
project: <project id, or ask>
send-to: orchestrator | worker
status: suggested | active
```
<prompt with {vars}>
```
````

- **Run**: match the name loosely and fill each `{var}` from the message. Ask for any that are missing. A project in the message overrides the template's. `send-to: orchestrator` (the default) goes through ao-orchestrator with the filled prompt as the request. `worker` starts one worker (ao-actions) only when the project has no live orchestrator, else it goes to the orchestrator too. Reply with `<host>/<session>`. A `suggested` template that ran fine: ask whether to mark it `active`.
- **List**: name, purpose, project and status, one line each.
- **Save**: turn the given prompt, or a session's first request (`aoapi GET "sessions/<id>/history?offset=<last>&limit=1"`), into a template with `{vars}` for the parts that change. Show it; write it after an OK.
- **Suggest**: when the user sends the same kind of request three or more times, offer a template with `status: suggested`. Once per kind.
- Edit or remove a template only when the user asks. Never put a key or password in a template.
