# Draft credential recovery

Decision: startup may recover only the draft gateway's own private configuration and auth directory. It must never discover or import native account files. Preserve existing SDK indexes so saved choices remain stable. Validate every source before changing anything, commit the encrypted batch before removing plaintext, and retain a durable source fingerprint for idempotent crash recovery. A tombstoned migrated account must stay deleted if stale source files reappear. Unsupported source shapes fail closed without removing their data.

- [x] M1: Supported draft keys and token files retain identity, disabled state, and credentials after encrypted migration and restart; source credential markers are removed only after durable commit.
  CHECK: env GOWORK=off go test -race ./internal/runner -run '^TestCredentialMigration' -count=1
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: Linux, fish, runner module; focused migration/vault races passed 20 repetitions (4.216s). Real runner integration passed 10 repetitions (4.940s), including migration, abrupt restart, two selected credentials, deletion, and another restart.

- [x] M2: Interrupted cleanup, changed sources, tombstone replay, invalid input, and unsafe files cannot lose or resurrect credentials.
  CHECK: env GOWORK=off go test -race ./internal/runner -run '^TestCredentialMigration' -count=20
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: Same focused 20-repeat run passed. Tests reject symlink/hardlink sources, malformed/unsupported records, closed storage, an active old runner, and changed source fingerprints; committed tombstones survive interrupted cleanup. The marker scanner now traverses nested directories and retains its positive control.

- [x] M3: Integrated startup and model/admission tests pass, and review confirms no native data discovery or account-selection mutation.
  EVIDENCE: Runner build/vet/full race exit 0 (race suite 2.707s). The integration test verifies 20 concurrent A/B requests, stable references after restart, removal of A without redirecting it to B, and a durable deletion after the next restart. Recovery reads only the supplied gateway root and never calls daemon selection storage. Private credential protocol 2 prevents attaching to an older runner. Adapter and service race suites pass. This is synthetic Linux evidence, not live provider or native-platform proof.

Review found an independent SDK error logger that persists failed request bodies despite request-log=false. The runner now supplies a nil logger through the public SDK factory. The integration test reproduces the failure without this option and requires the auth directory to remain empty after rejected requests.
