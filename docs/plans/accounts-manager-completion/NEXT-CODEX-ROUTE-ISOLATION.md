# Gates: managed Codex launch configuration

Base: `59434d18d194ef9453401f1688af222109f8949e`. Scope: the existing custom-provider terminal launch and restore boundary, before adding managed app-server execution. No protected native Chat, account-switching or Subscriptions file is in scope. Independent switching and containment acceptance remain prerequisites for expanding managed execution.

## Design decision

The native Chat driver is not a safe managed wrapper: its preflight reads ambient configuration, its raw persistent transport does not bind the route owner, and error cleanup uses a reusable session slot. Keep its current rejection for managed sessions. A future managed driver must use per-controller transport identity, scoped configuration, exact teardown and a separate capability probe. No availability flag is enabled by this slice.

The existing routed terminal builder also needs a closed configuration boundary. A non-nil but incomplete route currently becomes native argv. Reject invalid route material with a static error before returning any command. A valid route must use the private loopback HTTP origin and a syntactically valid dedicated environment variable. Never put the token value in argv or an error.

Request process-memory credential storage for managed launches only. Keep the selected custom provider and its explicit environment key. Preserve native launch bytes, the user's login, history location and stored credentials. Do not create a replacement home or silently move an existing thread. This is protection from accidental credential precedence, not a same-user security sandbox or a containment proof.

The documented credential-store selector includes a memory-only mode, and local administrative requirements can enforce authentication policy. An incompatible installation must fail, never silently choose another account. The installed-version experiment must inspect effective configuration as well as generated flags. [Configuration contract](https://learn.chatgpt.com/docs/config-file/config-reference), [managed requirements](https://learn.chatgpt.com/docs/enterprise/managed-configuration).

## Midpoint self-review

The first red log is `/var/tmp/pr-5769-next-79.wos4rh/codex-route-red.log`: both builders accepted invalid routes and omitted the memory-only override. The initial corrected matrix passes three race repetitions. The installed `0.153.4` check passes with a synthetic native file credential in a private home and a network-isolated child. It reads effective provider/storage values and both distinct endpoints, with native controls before and after.

Review corrections: route validation now precedes the missing-history restore branch, so malformed managed intent cannot become a fresh native launch. Coverage includes empty query/fragment markers, encoded paths, invalid ports, alternate origins and an attempt to reuse the native credential variable. Input values never enter errors. No child is launched by command construction.

This is a bounded configuration correction, not full authentication isolation. Enforced system policy, conflicting authentication environment variables/helpers, keyring behavior, version minimums, managed Chat, concurrent upstream requests, retirement and resume remain integration obligations. The future managed driver must inspect effective configuration before admitting a thread and must not assume a requested setting defeated enforced policy. No lifecycle or platform gate is cleared here.

## Acceptance

- [x] R1: launch and restore reject non-nil incomplete or unsafe routes without returning native argv; positive and native controls retain their intended mode.
  CHECK: go test -v -race -count=3 ./internal/adapters/agent/codex -run '^TestManagedRouteConfiguration'
  EXPECT: /ok\s+.*\/adapters\/agent\/codex/
  CWD: backend
  EVIDENCE: `/var/tmp/pr-5769-next-79.wos4rh/codex-route-red.log` preserves the pre-fix failures. `codex-route-final2-focused.log` passes all selected cases three times under race, exit 0, from backend through Fish and the credential-stripping wrapper. Exact commands and unchanged four-file hashes are recorded in `codex-route-final2-results.json` and `codex-route-final2-tested.sha256`.
- [x] R2: managed launch and restore explicitly select process-memory credentials and the exact route, while native commands are unchanged and values remain out of argv/errors.
  CHECK: go test -v -race -count=3 ./internal/adapters/agent/codex -run '^TestManagedRouteConfiguration'
  EXPECT: /ok\s+.*\/adapters\/agent\/codex/
  CWD: backend
  EVIDENCE: `codex-route-final2-focused.log` and `codex-route-final2-adapter-full.log`, both exit 0 with three race repetitions. No native credential or history path is changed by the production correction.
- [x] R3: the installed binary confirms effective managed configuration with conflicting synthetic native state, and native control remains distinct; no real provider traffic or credentials are used.
  EVIDENCE: `codex-route-final2-installed.log`, exit 0, 12 executed leaf cases across three race repetitions on `codex-cli 0.153.4`. A private home, explicit child environment and network namespace prevent real credential/provider use. Effective provider, store and endpoints are inspected through the actual protocol; native file-login controls precede and follow managed checks. No thread or model request is sent. This is not live A/B provider evidence.
- [x] R4: midpoint review, full affected package race, backend build/vet/lint and 91 protected hashes pass on the exact final source snapshot.
  EVIDENCE: `codex-route-final2-results.json` records all 14 commands passing with unchanged source hashes, including matching-shell parent and current full session-manager race, production switching race x3, backend build/vet, tagged vet, full and tagged lint, Windows amd64 and both Mac cross-compiles. Protected and generated manifests pass unchanged. Native execution is not claimed. Prior-seal archive integrity is checked again when freezing R5.
- [ ] R5: freeze exact correction, source and evidence manifests and request independent review before relying on the new boundary in managed Chat.
  EVIDENCE: pending.

## Verification limitation retained

The first broad session-manager run failed at `TestAccountsManagerSwitchHandoffRequiresRecordedOwner/ptyhost-v1:/cold_waiting/missing_identity`: the existing worker-completion helper reached its deadline. The same leaf then passed ten times on each tree. An initial parent full run used a different shell setting and is diagnostic only. The final parent/current full runs both pass with matching toolchain, process limit and shell settings.

This fixture uses another provider's recording stub and does not execute the changed Codex command builder. Its source is unchanged. No timeout was increased and no readiness or ownership condition was weakened. The earlier failure remains an unclassified timing observation, not a claimed environment diagnosis or corrected switching defect. Preserve it for integrated review.
