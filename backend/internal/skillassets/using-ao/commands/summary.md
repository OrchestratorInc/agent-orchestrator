# ao summary

Set the one-line summary on your kanban card. The human watches the board to
follow what each worker is doing right now, so a worker keeps its card current
for the whole task.

## Syntax

```text
ao summary "<text>"
ao summary --done "<text>"
```

## When to set it

- As soon as you start the task.
- Each time you move to a new step: reading a part of the code, changing a
  specific area, running tests or a build, fixing a failure, committing.
- Once more before you finish, with `--done`, in past tense.

Name the concrete thing in plain words, under 200 characters: "Adding backoff
tests for failed provider calls", not "Working on tests".

## Rules

- Run it as its own command: no `&&`, pipes, or redirects. Issue it in the same
  turn as your next tool call when you can.
- Updates are accepted at most once a minute. A refused update exits 0 and says
  so; do not retry it, carry on and set the next one at your next step.
- `--done` is always accepted.
- It is best-effort. If it fails, carry on.
