# ao pane

Show or change how this session's tabs are split in the AO desktop window.
The window can show tabs side by side or stacked: your own chat or terminal,
shell terminals, reviewers and open files. Use it when the user asks for a
split ("open a terminal next to the chat", "put the test output below") or when
showing two things at once helps them follow your work.

Split panes are a Developer Mode preview. The window refuses changes while
Split panes is off (Settings → General), and only changes the session that is
on screen. If a command says so, tell the user rather than retrying.

```bash
ao pane                                    # tabs, their keys, and the splits
ao pane split terminal                     # new shell terminal right of the tab on screen
ao pane split terminal --side bottom       # ... or below it
ao pane split file:src/app.ts --beside session
ao pane split <tab-key> --beside <tab-key> --side left
ao pane focus <tab-key>                    # bring a tab (and its split) on screen
ao pane remove <tab-key>                   # take a tab out of its split
ao pane ungroup                            # turn the split on screen back into tabs
```

## Tab keys

`ao pane` lists every tab with its key:

- `session`: your own chat or agent terminal.
- A shell terminal's handle id, for example `shellterm-…`.
- `reviewer:<id>` and `reviewer-chat:<id>`: the reviewer.
- `file:<path>`: an open file.

`split` also takes `terminal` (open a new shell terminal) and
`file:<path>` for a file that is not open yet, workspace-relative or absolute
inside this session's workspace.

## Rules

- Run `ao pane` first and use the keys it prints; do not guess handle ids.
- `--beside` defaults to the tab on screen and `--side` to `right`. Sides are
  `left`, `right`, `top` and `bottom`.
- A new terminal from `split terminal` is a normal shell tab; run commands in it
  only if the user asked for that.
- Every command prints the layout after the change. Add `--json` for the same
  data as JSON (`selected`, `shown`, `tabs`, `groups` with each pane's `x`,
  `y`, `width` and `height` as fractions).
- Errors are final answers from the window: `SPLIT_PANES_OFF`,
  `SESSION_NOT_ON_SCREEN`, `TAB_NOT_FOUND`, `NO_DESKTOP_WINDOW`. Report them to
  the user instead of looping.
