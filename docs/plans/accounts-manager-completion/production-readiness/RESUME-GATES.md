# Gates: remaining account implementation

Scope: execute RESUME-PLAN.md without weakening existing production acceptance gates. Shell: fish. Commands run from the repository root unless CWD specifies backend.

- [x] S1: the reproduced post-turn capture regression and real direct/fallback drain controls pass.
  CHECK: go test -tags=e2e -race ./internal/session_manager -run '^TestAccountsManagerDrainRealTitleOutput$' -count=3 -timeout=3m -v
  CWD: backend
  EXPECT: ok
  EVIDENCE: surface-title-red.log reproduces Unicode title text leaking into the composer. composer-intensity-red.log reproduces color payloads hiding real drafts. terminal-final-race.log passes both full affected packages three times. title-drain-final-production.log passes four direct/fallback cases three times (49.531s): active work holds the original account, settled empty input switches, real drafts preserve the source, and drain sends no input. These are real runtime processes with synthetic provider output, not a live account or desktop acceptance result.

- [x] S2: affected switch recovery and cancellation race tests pass after the correction.
  CHECK: go test -race ./internal/session_manager -run '^TestAccountsManager.*(Switch|Cancel|Handoff|Recovery)' -count=3 -timeout=8m
  CWD: backend
  EXPECT: ok
  EVIDENCE: switch-final-race.log, three repeats, PASS, 172.246s. Independent review of this correction remains pending.

- [ ] L1: managed Linux launch containment, exact retirement and coordinated deletion pass production crash/recovery controls and independent review.
  EVIDENCE: host-final-race.log reproduces all five positive failures in the three pre-existing exact-retirement groups. Uncontained owners are still refused; no deletion safety guard was weakened. M01/M02 remain open.

- [x] C0: the managed Codex adapter foundation preserves explicit routing, isolated profiles, history and managed host identity.
  CHECK: go test -tags=e2e -race ./internal/adapters/chatdriver/codexappserver -run '^TestManaged' -count=3 -v -timeout=4m
  CWD: backend
  EXPECT: ok
  EVIDENCE: first-slice-v1-codex-installed.log uses the installed binary, isolated network and synthetic HTTP endpoint, three repeats, 22.682s. Both requests overlap with distinct authorization; private history survives a new provider process; revoked A receives failure while B continues; native configuration remains unchanged; managed authorization never reaches the configured ambient proxy. The fixture supplies process lifecycle directly and does not certify production containment. managed-host-owner-red.log and managed-host-publication-red.log preserve the two ownership defects. managed-focused-race.log includes detached managed host identity and reattachment controls. managed-start-classification-red.log and managed-loopback-proxy-red.log preserve the additional midpoint findings. first-slice-v1-codex-race.log passes the full driver package three times on the sealed source (41.797s).

- [ ] C1: managed Codex Chat has isolated overlapping controllers, private profiles, history and exact retirement with no ambient or native fallback.
  EVIDENCE: C0 is a foundation only. The production registry and managed Chat admission guards are unchanged. Service generation propagation, exact contained retirement, switching/removal integration, additional directory/tool-server support and independent enablement review remain open. M05/M06 are not complete.

- [ ] R1: cold runner/vault recovery preserves settings, queues and authorization; eligible failed journals and explicit credential reconnect recover safely.
  EVIDENCE: pending; M03/M04/M06/M07 remain open

- [ ] V1: the final integrated source passes full verification, real desktop/live workflows and independent review.
  EVIDENCE: pending; no current snapshot completion claim

- [ ] V2: native Windows and both Mac architectures pass the required containment and desktop matrices.
  EVIDENCE: pending external native execution; cross-compilation cannot meet this gate

## Verification boundary

All logs named above are under `/var/tmp/pr-5769-implementation-79.5HnQwE`. Commands use `GOMAXPROCS=2`. Session-bound CLI tests also clear `AO_SESSION_ID`, `AO_PROJECT_ID`, `AO_BROWSER_CAPABILITY`, `AO_RUN_FILE` and `AO_DATA_DIR` in their child environment. The first full CLI run inherited worker credentials/context and failed three existing fixture expectations; cli-ports-clean-env-race.log passes without changing those fixtures. Earlier fixture path/cleanup and lint failures are retained and superseded by final logs, not counted as product failures or successful checks.

Final source: first-slice-v1-source.sha256, 18 files, SHA256 `5dbddfdfc98626cfc4874d8f5772a9a0e76e9b7ceb46c60df0bc65a8e5545b30`. Archive: first-slice-v1-source.tar.gz, SHA256 `2de498e276f1ae3c1da1eda173e229ecf43573107d8066fcc11f2cb6dd77a0a2`. Backend build/vet, tagged vet, API spec tests and changed-scope tagged lint pass. API/SQL artifacts and runner dependency files are unchanged. The protected audit verifies all 91 entries. Windows amd64 and both Mac architectures compile and vet only; native execution remains unverified.
