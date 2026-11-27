# 7. One unauthenticated automation hook route

Date: 2026-11-27
Status: Accepted

## Context

A "when called" automation starts a session when an outside service (GitHub,
Sentry, Linear, a script) sends an HTTP request to it. Those services cannot
hold the Remote access connection password, and they cannot pair like a phone.
They can only be given an address and, for most of them, a shared signing
secret.

AGENTS.md lets the LAN listener and the managed tunnel serve only routes behind
`authMiddleware`, with one exemption for `GET /api/v1/identity`
(see [0003](0003-unauthenticated-identity-probe.md)), and requires an ADR for
any further unauthenticated route.

## Decision

Exempt exactly one more route from `authMiddleware` on the LAN listener:
**`POST /hooks/{key}`**.

- **One path segment, POST only.** The key must match `[A-Za-z0-9_-]+`. A
  nested path, an encoded slash, or any other method still needs the password.
- **The key is the credential.** Each automation gets 32 random bytes
  (base64url) when it is created. An unknown key answers 404.
- **Optional signature.** When the automation has a secret, the request must
  carry an HMAC-SHA256 of the raw body in the configured header, compared in
  constant time, or it answers 403.
- **Bounded work.** The body is capped at 1 MiB, the filled prompt at 64 KiB,
  and the values hashed for a dedupe key at 8 MiB. At most 20 calls wait per
  automation before the route answers 429. A dedupe key, when set, drops
  repeat deliveries for seven days. A called run holds the automation only
  until its session launches, so the queue bounds the backlog, not how many
  sessions run at once; pausing drops calls not started.
- **Request data is data.** Values copied from the request into the prompt are
  wrapped in a random tag and the prompt says to treat them as data.

The route is reachable from outside the machine only while Remote access is on,
through the same LAN listener and tunnel as the app API.

## Consequences

Anyone who has an automation's address can start that automation, with the
prompt its owner wrote and request data filling only the placeholders. They
cannot reach any other route or read anything back beyond the run id, and
their data reaches the agent only inside the data tags. Adding a secret limits
callers to whoever holds it.

The hook path is not counted by the password lockout: it presents no password,
and a 256-bit key cannot be guessed.

This is not a general exemption mechanism. Any further unauthenticated route
still needs its own ADR.
