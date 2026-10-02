# Documentation coverage

Reviewed 3 October 2026. Public desktop baseline: [v0.13.3](https://github.com/Untrivial-ai/agent-orchestrator/releases/tag/v0.13.3) (published 1 October 2026 UTC). Contributor docs are checked against the current checkout; `main` already contains changes after the stable tag. Cloud requires both a source check and a deployed-environment check because account entitlement and provider configuration are external.

| Area | User guide | Source checked | Hands-on status |
| --- | --- | --- | --- |
| Desktop install, local projects, agent modes | Installation, Quickstart, Projects, agent catalog | Desktop renderer, CLI, daemon registry, stable release | Docs build and route checks; no fresh installer run in this review |
| Local Automations, Cues, browser, reports, recovery | Lifecycle automation, Dashboard, Examples, CLI, Review loop | Renderer controls and CLI/daemon handlers | Code-backed; provider-specific behavior varies |
| Notifications | Dashboard and notifier pages | `NotificationCenter.tsx` and daemon notification API | Single-feed behavior checked in source; no live toast acceptance |
| Connect Mobile | Connect Mobile | Desktop settings and daemon mobile bridge | **Separate phone app source, physical iOS/Android pairing, and off-Wi-Fi reconnect not checked**; no credential-bearing screenshots captured |
| Cloud sessions | Cloud sessions guide | Desktop Cloud gate/project/task UI and `cloud/` control plane | Production `/healthz` returned 200 on 3 October 2026; **authenticated production journey not run** |
| Cloud development/deploy | Cloud development, `cloud/README.md`, deployment runbook | Root scripts and `cloud/scripts/deploy-staging.sh` | Read-only script review; no staging/production deployment |
| Public links and translations | README, translated READMEs, docs site constants | Current public docs URL and official release/download pages | Link/build checks; marketing-site copy and Homebrew tap are separately owned |

Open verification before treating every flow as release-accepted:

- Pair a real iPhone and Android device on LAN and a secure remote path; record redacted screens that show no QR, password, host credential, or personal data. Check sleep/reconnect, push, removal, and version mismatch.
- Walk through Cloud sign-in, GitHub grant, provider connection, project creation, remote session, Files, terminal, reviews, and delete on a permitted test account. Record the deployed release, organization gates, and any quota or provider limitation. A health check alone does not cover this.
- Confirm marketing-site agent counts, download page, and Homebrew recipe in their owning repositories. The public manual links to the live catalog and download page, but this PR cannot change those external sources.
- Product fixes remain separate from documentation: the CLI currently ignores unknown `--config-json` keys ([#6169](https://github.com/Untrivial-ai/agent-orchestrator/issues/6169)) and drops `effort` in its project-config mirror ([#5954](https://github.com/Untrivial-ai/agent-orchestrator/issues/5954)). The manual states both limits until the CLI contract changes.

Do not interpret a checked source row as proof of a live device or authenticated service run. Update this file with the release and environment used when those tests are completed.
