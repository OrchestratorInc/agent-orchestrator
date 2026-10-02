# PC4 first-path self-review

Scope: session account panel and its public HTTP helpers. Settings removal and session-view integration have not started. Artifact root: `/tmp/pr-5769-pc4-79.hoSn6v`.

## Findings and corrections

1. Introduced type error: retry/cancel sent an empty object while the generated client declares a bodyless request. Removed the body without changing the reviewed API. `first-path-typecheck.log` records TS2322; `first-path-typecheck-fixed.log` records the successful rerun.
2. A prior terminal switch in a session snapshot could mask a newly accepted pending operation. Track the exact submitted operation separately from the committed binding. A dedicated operation read and cache preserve that distinction; an accepted response never changes the committed account.
3. A lost response previously permitted a different operation ID. Save only validated, non-secret request intent before sending. On remount, read the exact operation, never automatically resend. A missing-operation response permits only an explicit resend of the same body and ID. It does not imply cancellation or completion. A rejected revision clears the local intent, while ambiguous results remain blocked. Failure to preserve the reference blocks submission.
4. Client path validation admitted punctuation rejected by the server, and reads did not validate path IDs. Match the reviewed ASCII identifier contract before any network call. Validate returned session/operation ownership, modes, revisions, policies and phases.
5. An endpoint-shaped request ID could reach the UI. Reject colon-bearing diagnostic IDs and suppress raw transport/server messages. Keep ordinary bounded request IDs for troubleshooting.

`midpoint-red.log` records eight assertion failures and six passing controls before these corrections. SHA256: `daf3ef70c7d035784261df16b4e33977818a94e8fc51b96c5b2c25552b83e687`.

The initial correction briefly dereferenced an absent binding when both optional IDs were undefined. `midpoint-initial-green.log` is a failed intermediate run, not passing evidence. The null-safe correction passes in `midpoint-correction.log`.

## Focused verification

From `frontend/`, Fish launched:

```text
npm test -- src/renderer/components/SessionAccountControl.test.tsx src/renderer/lib/accounts-manager-controls.test.ts --maxWorkers=1
```

`midpoint-focused-1.log`, `midpoint-focused-2.log` and `midpoint-focused-3.log`: each exits zero, two files and 17 tests pass. These cover explicit choices, pending/committed separation, 501/503, stale revision, older snapshots, lost response plus remount, exact resend, retry/cancel ownership and body shape, invalid path IDs and diagnostic redaction. Responses are synthetic; this is not runtime or desktop evidence.

Final midpoint `npm run typecheck`: exit zero, `midpoint-typecheck.log`. The self-review corrections and expanded focused tests are included in this result. No remaining first-path type error was observed.

## Preservation and remaining scope

`midpoint-preservation.json` verifies corrected PC3 source/correction archives, PC2's 13 files, lifecycle/design's 113 files, protected 91 paths and the five-file guest design. Its SHA256 is `b904a0968d58e2ecc9dc3689dcd0f81d1f6017ee5c405718eb286122961ae177`. `midpoint-pc3-slice.log` and `midpoint-pc3-evidence.log` also verify the unchanged PC3 handoff and evidence seal.

The production daemon still omits the optional control-service dependency. Desktop session and coordinated-removal routes therefore return 501. This UI exposes that capability gap; it does not enable the service, invoke native switching, or silently fall back. A successful real switch/removal/restart cannot be certified on that wiring.

Next bounded work after this checkpoint: session-view entry, remaining negative/recovery controls, coordinated removal UI and existing sign-in coexistence, then complete frontend checks and actual isolated desktop evidence. Public contract, CLI, lifecycle, guest, native switching and Subscriptions remain frozen. No publication or release-completion claim.
