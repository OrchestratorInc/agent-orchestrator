# Registration fixture correction

Date: 2026-09-29. Follow-up to the complete integrated engine race run. Keep runtime policy, lifecycle code, protected paths and prior archives unchanged.

The batch-registration test blocks the second worker to enter a generic hook, then assumes that worker belongs to Account B. Concurrent workers can enter in either order. On failure its cleanup also releases the worker without joining the batch before unregistering global models. The focused three-test sequence reproduced 12 failed assertions in 30 executions before any source correction. Confirm the selected blocked owner using an assertion-only external overlay and compare an unchanged HEAD archive.

1. Preserve the full-run and focused red logs. Confirm that the blocked account can be A and check whether the two later model-list failures follow the failed fixture. Keep the media-relay result separate and reproduce it on the unchanged control.
2. Correct only the fixture: wait for the account whose own registration completes, assert its same-revision request does not wait for the other registration, and release and join every test-owned waiter before global cleanup. Exercise both enqueue orders. Do not serialize production workers, expand deadlines, skip assertions or change account-selection behavior.
3. Run a negative control which removes per-account completion from an external copy. The corrected test must fail while an unrelated registration remains blocked. Review the test's cleanup and its ability to catch that original production defect before broad reruns.
4. Run repeated focused races, the complete affected package race and the full engine/runner checks. Document the test-only vendor delta, preserve the previous seals, and create a separate correction manifest and handoff. Native-platform, containment and live-account gates remain open.

Evidence: `/var/tmp/pr-5769-integrated-79.6t2fcN`. No publication. The media-relay fixture is not modified by this plan.
