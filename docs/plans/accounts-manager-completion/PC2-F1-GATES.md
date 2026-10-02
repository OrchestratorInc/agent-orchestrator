# Gates: exact public request field names

Scope: correct reviewer82 F1 from /tmp/pr-5769-pc2-review-82.txt in PC2 only. Keep the CLI candidate parked, retain its red test, freeze lifecycle and guest-design files, and do not publish.

Plan: reproduce both orders and encodings of the Unicode alias on switch/removal and the history-choice flag; assert request correlation, zero delegation and unchanged SQLite journals/fences. Require exact decoded JSON tags before the existing struct decoder. Add the bounded adjacent tests, review the delta, run final checks, then freeze and stop for independent review.

Preservation before correction: the 113-file lifecycle/design and 91-file protected manifests match all live bytes. The two newly added guest-protocol drafts were outside those inventories. They are parked as /tmp/pr-5769-pc2-f1-79.E8WKAc/guest-protocol-deferred.patch and removed from the live tree to keep this slice PC2-only. The earlier accepted guest proposal and all existing archives remain unchanged; the parked drafts are recoverable and not part of this review.

- [x] F1: the Unicode regression fails on the reviewed implementation with deterministic HTTP and durable-state assertions
  EVIDENCE: /tmp/pr-5769-pc2-f1-79.E8WKAc/unicode-red.log, exit 1, 32 failed leaves. Both encodings and orders reached admission; SQLite cases demonstrated switch/removal journals and a deletion fence. Source correction followed this observed failure. The exact pre-correction source and regression are preserved alongside the log.
- [x] F2: exact tag validation rejects aliases and duplicates while preserving explicit zero and ordinary requests
  CHECK: go test -mod=readonly -p=1 -race ./internal/httpd/controllers -run '^TestAccountControl' -count=3 -timeout=3m
  EXPECT: ok
  CWD: backend
  EVIDENCE: /tmp/pr-5769-pc2-f1-79.E8WKAc/focused-controller-race.log, Fish, backend CWD, isolated environment, exit 0, 24.938s. All account-control tests passed three repeats with zero race warnings. Explicit removal zero and decoded escaped canonical tag controls pass.
- [x] F3: mutation bodies, identity mismatches, native mode, nullable fields and byte limits have boundary coverage
  EVIDENCE: the same focused race log covers forbidden recovery fields without mutation, foreign operation reads, changed operation/session identities after mutation, native success, nine nullable scalar fields, and 4095/4096/4097-byte requests on all six mutation forms. Runtime workers were not launched.
- [x] F4: final race, lint, API regeneration/drift, build, vet, frontend typecheck and preservation pass on one source snapshot
  EVIDENCE: REVIEW-82-PC2-F1.md records final exit-0 commands: full controller race count3 134.149s, spec/parity race count3 1.824s and 82.341s, API wiring race count3 1.251s, backend build and full vet, lint 0 issues, API regeneration/drift and frontend typecheck. Source manifest /tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-source.sha256 has 13 files, SHA256 a3618c483f9411738333c1c0870f921552dd9c58d3a5ba5232f084c6649bddc9. Both preservation manifests (113 and 91 files) and the five-file guest package match. CLI and new guest draft production paths are absent.
- [ ] F5: reviewer82 independently clears the corrected frozen PC2 boundary
  EVIDENCE: REVIEW-82-PC2-F1.md requests the bounded re-review. Four of five correction gates are locally met; no independent CLEAR yet and no abandoned gate. No CLI continuation before this verdict.
