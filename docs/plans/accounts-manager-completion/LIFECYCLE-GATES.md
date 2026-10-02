# Gates: lifecycle portability and shutdown closure

OWNS: accounts-manager/runner/internal/runner/**, backend/internal/adapters/chatdriver/persistenthost/**, docs/plans/accounts-manager-completion/LIFECYCLE*

Scope: joined refresh shutdown, supported-platform exact teardown, and durable owner-evidence recovery. D2 archives stay immutable. Public controls stay held.

- [ ] L1: automatic refresh cannot outlive runner closure or write after vault closure
  CHECK: go test -mod=readonly -p=1 -race -count=3 -timeout=120s ./internal/runner -run '^TestCredentialAutoRefresh' -v
  EXPECT: PASS
  CWD: accounts-manager/runner
  EVIDENCE: auto-refresh-red-v2.log and auto-refresh-race-v2.log under /tmp/pr-5769-lifecycle-79.tRGYkc; expanded admission/policy cases and final full runner checks remain open

- [ ] L2: malformed and interrupted ownership evidence cannot acknowledge retirement
  CHECK: go test -mod=readonly -p=1 -race -count=3 -timeout=120s ./internal/adapters/chatdriver/persistenthost -run '^TestProviderOwnerEvidence' -v
  EXPECT: PASS
  CWD: backend
  EVIDENCE: owner-incomplete-red.log, owner-duplicate-red.log, owner-duplicate-evidence-race-v2.log under /tmp/pr-5769-lifecycle-79.tRGYkc; complete interrupted-write and filesystem recovery matrix remains open

- [ ] L3: supported non-Linux platform mechanisms preserve exact ownership and unrelated processes
  EVIDENCE: HOLD. Windows birth cut is red-to-green under Wine, not native Windows. Broader recovery controls cannot certify job-limit evidence under Wine's zero-filled query stub. macOS orphan recovery and escaped descendants remain unresolved. See LIFECYCLE.md.

- [ ] L3a: escaping descendants cannot survive a successful retirement acknowledgement or account deletion
  CHECK: go test -mod=readonly -p=1 -race -count=3 -timeout=120s ./internal/adapters/chatdriver/persistenthost -run '^TestProviderOwnerEscapedDescendant' -v
  EXPECT: PASS
  CWD: backend
  EVIDENCE: owner-escaped-descendant-red.log records both exact retirement and SQLite-backed account deletion succeeding while the escaped child lives; implementation remains HOLD, not waived by D2's bounded group proof

- [ ] L4: affected packages pass bounded serial race, build, vet and changed-scope lint checks
  EVIDENCE: Linux ownership/host matrix passes three race repetitions in 59.641 s; refresh matrix passes three in 8.305 s; Linux package build/vet and Windows x64 compilation/vet pass. Full affected verification and lint remain open. Windows native execution is not established by Wine.

- [ ] L5: immutable review freeze includes exact manifests, 91 protected paths, historical R4 hashes and explicit remaining release gaps
  EVIDENCE: latest instruction authorizes only the bounded F2/F3 freeze tracked in LIFECYCLE-WINDOWS-EVIDENCE-GATES.md; full lifecycle freeze remains pending F1, moved-descendant, native platform and remaining recovery requirements

- [ ] L6: independent reviewer82 clears the exact lifecycle snapshot
  EVIDENCE: pending, public controls remain held
