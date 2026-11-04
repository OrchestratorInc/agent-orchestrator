# Test onboarding recovery

This change covers local and remote-host clone onboarding. Credential setup and
Git or agent installation flows are unchanged.

## Automated checks

From the repository root:

```bash
go -C backend test ./internal/service/project ./internal/httpd/...
npm --prefix frontend run test -- src/renderer/hooks/usePreparedClone.test.ts src/renderer/components/CloneRepositoryDialog.test.ts src/renderer/components/CreateProjectFlow.test.tsx src/renderer/components/CreateProjectAgentSheet.test.tsx src/renderer/i18n/instance.test.ts
```

The Go tests use local repositories and a test Git executable. They cover temporary
connection failures, retries, cancellation cleanup, destination collisions, and a
lost registration response without contacting a remote host.

To watch the renderer flows in a browser:

```bash
cd frontend
export PLAYWRIGHT_BROWSERS_PATH="$HOME/.ao/dev/onboarding-recovery/browsers"
npx playwright install chromium
AO_E2E_PORT=5876 npm run test:e2e -- e2e/clone-recovery.spec.ts --headed --workers=1
```

Add `--debug` to step through each interaction. These browser tests inject HTTP
responses and the desktop bridge. They verify the real renderer and API client,
but do not launch Electron or clone a repository. The Go tests cover those service
boundaries separately.

## Manual desktop checks

Use an isolated checkout and scratch AO data as described in `AGENTS.md`. In that
checkout, run:

```bash
bash test/onboarding/create-fixtures.sh
```

The command creates fixtures under `~/dev/ao-onboarding-demo/fixtures` and prints
a new fixture directory and the exact URL and destinations to
paste. It creates a local repository, an unrelated folder with a file to preserve,
and an existing checkout of the same repository. Each run uses a new directory.

| Flow | Steps | Expected result |
| --- | --- | --- |
| Repository URL | Clone a public GitHub repository using its normal page URL, with no `.git`. Use a fresh destination. | AO submits the normalized clone URL automatically. No URL conversion prompt. SSH and file URLs keep their transport. |
| Conflicting folder | Paste the fixture repository URL and its conflicting destination. | Continue is disabled before cloning. The message names the existing folder. Choose another destination to continue. `keep.txt` stays unchanged. |
| Existing checkout | Use the fixture repository URL and existing-checkout destination. Click Use existing checkout. | AO opens the normal import/setup flow without cloning, fetching, resetting, or deleting local files. |
| Leave completed clone | Clone into the available destination, then click Back from project setup. Enter the same URL and destination again. | The checkout stays on disk. AO offers Use existing checkout; continuing through reuse does not clone again. |
| Reuse validation retry | Simulate a failed imports/validate response while using an existing checkout, then restore the response and retry reuse. | Clone inputs and reuse controls stay visible after failure. Successful retry opens setup without a second clone. |
| Slow clone | Launch with the `slow` fault below. Use the fixture URL and available destination. | The dialog shows clone activity and elapsed seconds. It remains active past 60 seconds and eventually reaches project setup. |
| Cancel | Start another slow clone in a fresh destination. Click Cancel clone while it is running. | Cloning stops. Inputs stay filled and Retry becomes available. No partial final checkout or `.ao-clone-*` folder remains after the daemon finishes cancellation cleanup. |
| Temporary connection failure | Launch with the `retry` fault and a new fixture directory. Clone into its available destination. | AO retries twice, then succeeds on attempt three without user input. |
| Persistent connection failure | Launch with the `offline` fault. Start a clone. | AO stops after three attempts, explains the connection failure, and offers Retry. URL and destination stay editable. |
| Failure after cloning | Run the first browser test with `--debug`. It rejects project registration after a successful clone. Click Retry project setup, then Keep checkout and close. | Registration alone is retried. The clone count remains one. Keeping the checkout releases AO's cleanup ownership; reopening the same destination offers explicit reuse rather than taking ownership again. |
| Validation retry | Run the focused test named `keeps a successful clone and retries validation without cloning again`. | A failed checkout check retries against the same clone, including destinations entered with `~`. |

To inject clone faults into the isolated desktop build, replace the fixture path
below with the directory printed by the fixture command. Choose `slow`, `retry`,
or `offline`. Restart this dev instance to change the fault.

```bash
export AO_ONBOARDING_FIXTURES="$HOME/dev/ao-onboarding-demo/fixtures/run.REPLACE_ME"
export AO_ONBOARDING_REAL_GIT="$(command -v git)"
export AO_ONBOARDING_FAULT=slow
cd frontend
env -u AO_PORT \
  PATH="$AO_ONBOARDING_FIXTURES/bin:$PATH" \
  AO_DATA_DIR="$HOME/.ao/dev/onboarding-recovery/data" \
  AO_RUN_FILE="$HOME/.ao/dev/onboarding-recovery/running.json" \
  AO_DEV_ELECTRON_DIR="$HOME/.ao/dev/onboarding-recovery/electron" \
  npm run dev
```

Use a fresh fixture directory for another `retry` test, because its counter records
completed attempts. The wrapper affects only clones in this dev process. Do not
put it on your permanent PATH or use it with your normal AO data.

The clone request stays attached to the client and cancels on disconnect. It is
not a durable background job. A flow holding its original preparation token can
retry validation without cloning again. A later flow uses **Use existing checkout**;
an old ownership marker never grants a new request permission to delete files.
Older daemons without destination preflight keep completed checkouts when leaving
onboarding, because their cleanup API cannot safely release ownership.
Back and Close also preserve completed checkouts on current daemons. Cancellation
only cleans the in-progress clone's temporary staging folder.
