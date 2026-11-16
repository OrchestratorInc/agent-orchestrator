# ao pane

Show or change how this session's tabs are split in the AO desktop window,
and run commands in this session's terminal tabs where the user can watch.
The window can show tabs side by side or stacked: your own chat or terminal,
shell terminals, reviewers and open files. Use it only when the user asks for
a split ("open a terminal next to the chat", "put the test output below") or
asks to run something where they can watch. Never open, move or close splits or
terminals on your own initiative: the window is the user's, and they did not
ask for a layout change.

When the user asks you to run something "in a terminal", "on the right", or
anywhere they can see it, run it in a terminal tab with `ao pane run` or
`ao pane split terminal --run`. Do not run it in your own shell instead: they
see only the terminal tab.

Split panes are a Developer Mode preview. The window refuses changes while
Split panes is off (Settings → General), and only changes the session that is
on screen. If a command says so, tell the user rather than retrying.

```bash
ao pane                                    # tabs, their keys, and the splits
ao pane split terminal                     # new shell terminal right of the tab on screen
ao pane split terminal --side bottom       # ... or below it
ao pane split terminal --run "ls -la"      # new terminal on the right, run a command in it, print its output
ao pane run <terminal-key> "npm test"      # run a command in an existing terminal tab, print its output
ao pane read <terminal-key>                # print what a terminal tab shows now
ao pane send --no-enter <terminal-key> y   # type without Enter (prompt answers, TUI keys)
ao pane interrupt <terminal-key>           # Ctrl-C
ao pane close <terminal-key>               # close a terminal tab, running or exited
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

## Running commands in a terminal tab

- For `run` and `send`, everything after the tab key is typed as is, dashes
  included (`ao pane run <key> ls -la`). Put flags such as `--timeout`,
  `--lines` or `--no-enter` before the tab key.
- `run` and `split --run` type the command and press Enter, wait until the
  screen stops changing (`--timeout`, 15 s by default), then print the last
  `--lines` (60) lines. A long-running command (a dev server, a watcher) keeps
  running after `run` returns. Check on it with `ao pane read`, and stop it with
  `ao pane interrupt`.
- When the user asks you to close or kill a terminal, use `ao pane close`. It
  closes the tab and collapses its split. A terminal whose shell exits (`exit`,
  Ctrl-D) closes on its own.
- The terminal is a normal shell in this session's workspace. Commands run as
  the user and they see every keystroke, so run only what the user asked for or
  what the task needs.
- `ao pane` works only from inside your session: like `ao browser`, it proves
  the session with the `AO_BROWSER_CAPABILITY` AO gives your agent. It can't
  act on another session.
- Only this session's own terminal tabs can be driven. The agent tab, reviewers,
  other sessions' terminals and AO's login terminals are refused.

## Rules

- Run `ao pane` first and use the keys it prints; do not guess handle ids.
- `--beside` defaults to the tab on screen and `--side` to `right`. Sides are
  `left`, `right`, `top` and `bottom`.
- A new terminal from `split terminal` is a normal shell tab; run commands in it
  only if the user asked for that.
- `ao pane` (also `ls`), `split` (without `--run`), `focus`, `remove` and
  `ungroup` print the layout after the change. Add `--json` for the same data
  as JSON (`selected`, `shown`, `tabs`, `groups` with each pane's `x`, `y`,
  `width` and `height` as fractions). `read` prints the terminal's screen (as
  JSON with `--json`), and `run` and `split --run` print the terminal's last
  lines. `close` prints a one-line confirmation, and `send` and `interrupt`
  print nothing.
- Errors are final answers: `SPLIT_PANES_OFF`, `SESSION_NOT_ON_SCREEN`,
  `TAB_NOT_FOUND`, `NO_DESKTOP_WINDOW`, `PANE_TERMINAL_NOT_FOUND`,
  `PANE_TERMINAL_EXITED`. Report them to the user instead of looping.
- `run`, `read`, `send` and `interrupt` work without the window. They need only
  the terminal tab's key.
