---
name: ao-usage
description: >-
  Orchestrator's token usage and estimated cost by agent, model, day and
  session, plus how full each login's five-hour and weekly limits are. Only
  when the user asks: "usage", "costs", "what did I spend", "how many tokens",
  "how close am I to my limit", or the weekly usage routine. Never in digests
  or unprompted reports.
---
Follow the ao-rules skill. Read-only. Run on every host, in parallel.

## Tokens and cost
`<days>` defaults to 7; "this month" is today's day of the month in `<tz>` (the 14th: 14, which starts on the 1st). `<tz>` is the user's IANA timezone.
```sh
f(){ <aoapi helper>; aoapi GET "usage/report?days=<days>&tz=<tz>" | python3 -c '
import json,sys,collections
d=json.load(sys.stdin); rows=d.get("rows") or []
tok=lambda r: r["inputTokens"]+r["cacheReadTokens"]+r["cacheWriteTokens"]+r["outputTokens"]
def by(k):
    t=collections.Counter(); c=collections.Counter()
    for r in rows: t[r[k]]+=tok(r); c[r[k]]+=r.get("costNanos",0)
    return [(x, t[x], c[x]/1e9) for x in sorted(t, key=lambda x:-c[x])]
print("total tokens=%d cost=$%.2f unpriced_events=%d" % (sum(map(tok,rows)), sum(r.get("costNanos",0) for r in rows)/1e9, sum(r["events"]-r.get("pricedEvents",r["events"]) for r in rows)))
for k in ("harness","model"):
    for x,t,c in by(k)[:10]: print(k, x, t, "$%.2f" % c)
day={x:(t,c) for x,t,c in by("period")}
for x in d.get("periods") or sorted(day): t,c=day.get(x,(0,0)); print("period", x, t, "$%.2f" % c)
for s in sorted(d.get("sessions") or [], key=lambda s:-s.get("costNanos",0))[:5]: print("session", s["sessionId"], (s.get("title") or "")[:50], "$%.2f" % (s.get("costNanos",0)/1e9))'; }; f
```
Add `&projectId=<id>` for one project. Per host and combined: total tokens and cost, then by agent, the top models, by day, and the five costliest sessions as `<host>/<session>`. Every figure is an estimate at Orchestrator's prices (Usage settings in Orchestrator): a subscription isn't billed per token. Say how many events had no price.

## Limits
```sh
f(){ <aoapi helper>; aoapi GET 'usage/limit-windows?days=7' | python3 -c '
import json,sys
for l in json.load(sys.stdin).get("logins") or []:
    for w in l.get("windows") or []:
        if w.get("state")=="open": print(l.get("provider"), l.get("plan"), w.get("label"), "%s%%" % w.get("peakUsedPercent"), "resets", w.get("resetsAt"))'; }; f
```
One line per login and open window: how full it got and when it resets, in the user's timezone. Name the login by provider and plan, never by email. Warn at 80% or more.

## Weekly usage routine
Only when the user asks for it: Mondays at 9:00, runs "Tokens and cost" for the last 7 days and "Limits", in at most 6 lines.
