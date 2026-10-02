# Device transport increment

Contract: the runner remains the only durable writer. The SDK device authenticator runs privately, returns one authenticated encrypted result over an anonymous pipe, and cannot publish until its durable operation remains admitted. A fresh transport key travels only through private standard input. Provider output is never forwarded to logs.

Implementation order: replace the file-store subprocess, connect the managed coordinator, exercise completion/cancellation/failure and malformed transport, review process cleanup, then rerun runner build/vet/full race tests.

- [x] D1: Device output contains no credential markers and rejects wrong keys, tampering, duplicate results, oversized frames, and missing completion.
  CHECK: env GOWORK=off go test -race ./internal/runner -run '^TestDevice' -count=1
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: Linux, fish, runner module, isolated credential-free environment; focused device race tests and the final full race suite exit 0. Subprocess tests cover cancellation before instructions and after instructions. Encryption scan includes a plaintext positive control.

- [x] D2: Managed device completion follows durable commit; cancellation, failure, shutdown, and late completion cannot publish an account.
  CHECK: env GOWORK=off go test -race ./internal/runner -run '^TestManagedDevice' -count=20
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: Linux, fish, runner module; 100 managed browser/device race repetitions exit 0 (2.406s). Final full race suite exit 0. Immediate completion also returns the operation identifier instead of an ambiguous startup error.

- [x] D3: Complete runner build, vet, and race suite passes after boundary review.
  CHECK: env GOWORK=off go build ./...; and env GOWORK=off go vet ./...; and env GOWORK=off go test -race ./... -count=1
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: Linux, fish, runner module, isolated credential-free environment; build and vet exit 0, full race suite exit 0 (2.707s) after the SDK error-log bypass was disabled.

- [x] D4: Manual review confirms no file token store, credential-bearing process argument/environment, raw provider error, or unmanaged worker remains on the enabled device path.
  EVIDENCE: The child calls the SDK authenticator directly. Fresh transport keys use anonymous stdin, encrypted bounded results use captured stdout, stderr is discarded, and provider errors are replaced by fixed codes. No transport key or result is added to argv/environment. Parent-owned cancellation closes and joins the subprocess. The managed worker commits through the vault operation and the coordinator joins it on shutdown. Runtime source and synthetic process tests were reread after the passing full suite.

Live provider login and native platform process/permission behavior remain separate release gates. Synthetic tests cannot establish those outcomes.
