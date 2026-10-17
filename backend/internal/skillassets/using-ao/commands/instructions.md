# ao instructions

Read or save the standing instructions AO adds to new sessions. The user's instructions go into every new worker and orchestrator in every project. A project's instructions go into new sessions in that project. A running session keeps the instructions it started with.

```text
ao instructions show [--project <id>] [--json]
ao instructions set --file <path|-> [--project <id>] [--revision <n>] [--json]
```

Without `--project`, both commands use the user's own instructions. `show` prints the text. `show --json` also prints:
- `revision`, to pass to `set --revision`;
- `protectedLines`, the lines a dreaming save must keep;
- `removedLines`, lines people deleted.

`set` replaces the whole text with the file's contents; `-` reads stdin. With `--revision`, the save fails if someone saved since that revision.

Inside an AO session you can read the instructions but not change them: only dreaming may save from a session. This stops a hostile file or web page from planting instructions in every later session. If the user asks you to remember a standing preference, tell them either:
- dreaming will learn it from what they typed once this session ends;
- or they can add it themselves on the Automations page.

The desktop app shows and edits both sets of instructions at the top of the Automations page. A dreaming automation updates them after each session ends; see [automation.md](automation.md).
