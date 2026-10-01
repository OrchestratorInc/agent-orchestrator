# Session sharing (deep links)

An owner shares one cloud session with one person through a single-use
`ao-app://share/<orgId>/<linkId>#<secret>` link. A share is fully interactive
unless the owner ticks **Read-only** in the Share dialog:

| Access           | Grant                        | Recipient can                                                  |
|------------------|------------------------------|----------------------------------------------------------------|
| **Read-only**    | `viewer`, capped read-only   | watch (and scroll) the terminal, chat history, and files       |
| **Full** (default) | `editor`, session's own mode | also message the agent from the prompt box and edit files      |

A recipient never types raw keystrokes into the owner's terminal. Two people
typing into one PTY interleave their characters on the same prompt line, so the
recipient works through the message box docked under the terminal (the same
composer as Chat). Each message reaches the control plane whole
(`POST .../sessions/{session}/messages`): an idle agent gets it typed into its
terminal as one unit, and a busy agent gets it after the running turn ends. The
worker holds the PTY's write lock across the prompt, its Enter delay, and the
Enter, so the owner's live keystrokes cannot split it. The owner keeps the raw
terminal and gets the same box.

At either level the recipient can never delete, restore, or re-share the
session. It stays the owner's.

The recipient can **remove a shared session from their list** (sidebar row →
the leave icon, or right-click → **Remove from my list**). That calls
`DELETE /api/cloud/v1/shared/grants/{grantId}`, which revokes only the
caller's own grant (audited as `share.left`). The owner's session and other
collaborators are untouched, and getting it back needs a new link.

## Flow

1. Owner: session ⋮ → **Share…** → enter the recipient's email → optionally
   tick **Read-only** →
   `POST /api/cloud/v1/orgs/{org}/sessions/{session}/share-deeplinks`.
   The control plane checks the caller is an org member who created the session,
   generates a 256-bit secret, stores only `sha256(secret)`, and writes a
   `share.created` audit event. The link carries the chosen access level, is bound to the
   recipient email, single use, and expires in 10 minutes. It is shown once as
   text and a QR code.
2. Recipient opens the link. Electron main validates it strictly
   (`frontend/src/shared/share-deeplink.ts`), queues it, and never redeems it.
3. The renderer asks the recipient to sign in if needed, then calls
   `POST /api/cloud/v1/share-deeplinks/preview` (does not consume the link) to
   show a consent dialog: who is inviting them, to which session, at which
   access level, and when it expires.
4. **Accept** → `POST /api/cloud/v1/share-deeplinks/redeem`. In one transaction,
   with the link row locked, the control plane checks the secret hash, status,
   expiry, recipient email, and that the caller is not the owner. It then flips
   the link `active → redeemed`, creates a grant at the link's access level,
   and writes `share.redeemed`. **Decline** grants nothing.
5. The session appears under **Shared with me** in the sidebar, badged
   **Read-only** or **Shared** (fully interactive, through the message box).

Every refusal (wrong secret, wrong account, expired, already used, self-redeem,
malformed input) is the same `403 share_denied`. The specific reason is written
only to the audit log as `share.denied`. Preview and redeem are rate limited to
10 attempts per user per minute.

## Local demo with two accounts

`npm run cloud:local` seeds two accounts on the Docker control plane:

| Role      | Email               | Password             |
|-----------|---------------------|----------------------|
| Owner     | `dev@local.test`    | `localdevpass123`    |
| Recipient | `viewer@local.test` | `localviewerpass123` |

### Scripted (no UI)

```bash
bash cloud/scripts/share-demo.sh              # full flow + every denial / read-only check
bash cloud/scripts/share-demo.sh --mint-only  # print a fresh link for dev@ -> viewer@
```

### In the desktop app, one window

1. `AO_CLOUD_OFFERING=on AO_CLOUD_CONTROL_PLANE_URL=http://127.0.0.1:8081 npm run dev`
   from `frontend/`, and sign in as `dev@local.test`.
2. Open a cloud session → ⋮ → **Share…** → `viewer@local.test` (tick **Read-only**
   for a view-only link) → copy the link.
3. Sign out, then sign in as `viewer@local.test`.
4. Click your email at the bottom of the sidebar → **Open share link…** → paste
   the link → **Open**. (Opening it through the OS with `xdg-open`/`open` also
   works, but only if this build owns the `ao-app://` scheme. An installed AO
   AppImage or app usually does, and then the link goes there instead.)
5. Accept. The session opens under **Shared with me**.

### In the desktop app, two windows side by side

Run a second dev instance with its own Electron profile, cloud sign-in, and
daemon, so each window holds a different account:

```bash
cd frontend
AO_DEV_ELECTRON_DIR=$HOME/.ao/dev-viewer/electron \
AO_DEV_CLOUD_DIR=$HOME/.ao/dev-viewer \
AO_DATA_DIR=$HOME/.ao/dev-viewer/data \
AO_RUN_FILE=$HOME/.ao/dev-viewer/running.json \
AO_PORT=3003 \
AO_CLOUD_OFFERING=on AO_CLOUD_CONTROL_PLANE_URL=http://127.0.0.1:8081 \
npm run dev
```

Sign in as `viewer@local.test` there. The OS sends `ao-app://` links to
whichever instance registered the scheme last, so for the side-by-side demo,
deliver the link straight to the viewer instance by passing it on that
instance's command line, or use the one-window flow above.
