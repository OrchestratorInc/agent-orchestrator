# ao remind

Schedule a one-shot follow-up into your own session ("wake me later"). When
it is due, AO delivers the message back into this same conversation as a new
turn, with your context intact. Use it instead of keeping your turn open and
polling with `sleep` for something that cannot notify you: a nightly build, a
release, a deploy, or a metric that needs time to collect data.

## Syntax

```text
ao remind --at <time> --message <text> [--name <label>] [--json]
ao remind ls [--json]
ao remind cancel <id>
```

`--at` takes an RFC 3339 time (`2026-11-01T04:00:00Z`), a delay (`+30m`,
`+2h`, `+1d`), or a local clock time (`04:00`, its next occurrence). A
follow-up must be at least 1 minute and at most 30 days away, and a session
can have at most 10 pending. Write `--message` for the context you are in now:
it is all you will see when the follow-up arrives.

Delivery never interrupts. In a Chat session the follow-up queues behind a
running turn; a hibernated session wakes to take it; a terminal session waits
for its running turn to finish. If the session was archived or killed first,
the follow-up is dropped. The user sees pending follow-ups above the composer
and can cancel them there.

The delivered turn starts with `⏰ Scheduled follow-up` and says that you
scheduled it, not a human. After several self-scheduled follow-ups in a row
with no human message, it also says so: stop checking and tell the user what
you are waiting on.

Do not schedule check-ins for things that already report back to you, such as
worker reports (`ao report`) or PR events. Space repeated checks further
apart, and stop after a few with no change. Chat agents also have the same
commands as the `remind`, `remind_list`, and `remind_cancel` tools of AO's MCP
server.

**Examples:**

```bash
ao remind --at +30m --message "Check the CI of the worker's PR and merge it if green."
ao remind --at 04:00 --name "Nightly" \
  --message "Verify ao-electron after the nightly build, then update the tracker."
ao remind ls
ao remind cancel rem_0b5c…
```
