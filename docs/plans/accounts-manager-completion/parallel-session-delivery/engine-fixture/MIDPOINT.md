# Fixture midpoint review

The prior fixture conflated worker order with account identity. The correction observes the service's per-account completion channels, requests the completed account's same revision while the other worker stays blocked, and verifies that the blocked account still has an unacknowledged fence. Reversing enqueue order exercises both batch layouts. An external control must withhold per-task completion and fail this assertion before broader validation.

Cleanup releases the test-owned barrier and joins both the batch and any same-revision waiter before clearing the global hook. Registry cleanup was registered first and therefore runs after these joins. Cancellation is scoped to this helper; no parallel subtests share the hook. The existing two-second assertion bound is unchanged. Cleanup timeout remains a test failure, not a skip or an accepted result.

Only the fixture and upstream-delta documentation change. Production auth synchronization, model registry, account selection, retirement and provider code are untouched. Earlier source archives remain immutable; the new integrated manifest must declare the additional test and updated upstream documentation explicitly.

The focused ten-repeat race run is green. No independent verdict is implied by this self-review. The media-relay timeout reproduces on unchanged HEAD and is not modified by this correction.
