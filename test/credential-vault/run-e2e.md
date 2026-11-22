# Credential vault end-to-end check

Proves an agent can sign in with a saved password and TOTP code while the
secret never reaches the agent, the CLI, logs, or AO's data dir.

Needs one running dev build of this branch (with its daemon, the
`agent-browser` runtime from `npm run browser-runtime:prepare`, and the
approval prompt), and a project in it whose repo has a remote.

## 1. Demo site with a random secret nobody sees

```bash
mkdir -p /tmp/vault-e2e && cd /tmp/vault-e2e
umask 077
node -e '
const c=require("crypto"),a="ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";let s="";
for (const b of c.randomBytes(20)) s+=a[b%32];
require("fs").writeFileSync("secret.env",
  `DEMO_PASSWORD=Vlt-${c.randomBytes(9).toString("base64url")}-9!\nDEMO_SEED=${s}\n`,{mode:0o600});'
(set -a; . ./secret.env; set +a; exec node <repo>/test/credential-vault/demo-site.js) > site.log 2>&1 &
```

`http://localhost:4173/login` asks for `alice@example.test` + the password, then
`/2fa` asks for the TOTP code, then `/welcome` greets the user.

## 2. Save the credential in Settings › Browser › Saved passwords

Add password: Sites `http://localhost:4173`, Username `alice@example.test`,
Password and 2FA setup key from `secret.env` (paste them; do not echo them in a
terminal an agent can read), Name `Demo app`. Screenshot the list row.

## 3. Let an agent sign in (start the screen recording first)

Spawn a Claude Code worker in the project with this prompt:

> Use the Orchestrator Browser (`ao browser`) to sign in to
> http://localhost:4173/login. A password and 2FA key are saved in Orchestrator;
> never ask me for them and never type a password yourself. Run
> `ao browser login http://localhost:4173`, submit; on the two-factor page run
> it again and submit. Stop on the Welcome page and tell me where you ended.

Expected:
- The approval prompt appears once, naming the session, `http://localhost:4173`
  and "Demo app" / `alice@example.test`. Choose **Always allow on this site**.
- The agent's `login` prints `Signed in fields username, password on
  http://localhost:4173 (••••)`, then `totp` on `/2fa`, with no second prompt.
- The page reaches `/welcome`; `site.log` shows `POST /login ok`, `POST /2fa ok`.
- Settings › Browser › Sign-in activity lists two `Filled` rows; the approvals
  log lists one `allow-site` then `site-allowed`.

Negative checks, same session:
- `ao browser open https://example.com && ao browser login http://localhost:4173`
  fails: the page is not on that origin.
- `ao browser open http://127.0.0.1:4173/login && ao browser login` fails: no
  saved credential (127.0.0.1 is a different origin from localhost).
- `ao browser get value <password-ref>` and `ao browser snapshot` on the login
  page after a fill show `••••`, never the password.

## 4. Prove nothing leaked

```bash
<repo>/test/credential-vault/check-no-leak.sh /tmp/vault-e2e/secret.env \
  "$AO_DATA_DIR" ~/.ao/dev ~/.claude/projects "<the worker's transcript dir>"
```

It searches every file (binary included) for the password, the seed and their
URL, form, JSON and base64 encodings, and prints only file names. Exit 0 means
every requested path was fully searched and nothing was found; 1 means a hit;
2 means the scan was incomplete (a missing path or an unreadable file), which
is not a pass. `sh test/credential-vault/check-no-leak.test.sh` self-tests it.
