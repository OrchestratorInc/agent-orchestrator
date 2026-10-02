# Forced refresh increment

Contract: concurrent manual checks for one account share one provider refresh. At most four independent manual refreshes run at once. A caller can stop waiting without cancelling other callers. Work has a ten-second deadline and is cancelled/joined on runtime shutdown. SDK generation checks and durable admission reject results arriving after removal or replacement. Raw provider errors never leave the runner.

- [x] R1: Concurrent callers share a result, independent checks are bounded, and cancellation/deletion/shutdown cannot publish an uncommitted credential.
  CHECK: env GOWORK=off go test -race ./internal/runner -run '^TestCredentialRefresh' -count=20
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: Fish, runner module, credential-free environment, GOWORK=off. Refresh race tests passed 100 repetitions (5.121s), including a caller deadline with a deliberately late provider result. These are synthetic refreshes.

- [x] R2: The full runner build, vet, and race suite passes after refresh integration review.
  CHECK: env GOWORK=off go build ./...; and env GOWORK=off go vet ./...; and env GOWORK=off go test -race ./... -count=1
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: Fish, runner module, credential-free environment, GOWORK=off. Build, vet, and full race suite exited 0; runner package ok in 2.791s after reconnect integration.

The SDK owns refresh mechanics and its automatic refresh scheduler. This increment coalesces manual checks; live provider expiry/rotation remains separate release evidence.
