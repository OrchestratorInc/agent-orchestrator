# Security policy

Orchestrator.inc runs coding agents that can execute commands on your machine,
and it exposes local, LAN, and tunnelled endpoints for the desktop and mobile
apps. We take reports about any of these seriously.

## Reporting a vulnerability

**Please do not open a public GitHub issue, discussion, or pull request for a
security problem, and do not post it on Discord.**

Report it privately through GitHub:
[**Report a vulnerability**](https://github.com/OrchestratorInc/agent-orchestrator/security/advisories/new)
(repository **Security** tab → **Report a vulnerability**).

<!-- TODO(maintainers): add a security contact email for reporters who cannot use GitHub. -->

Include as much of the following as you can:

- the affected component (desktop app, `ao` CLI/daemon, mobile app, Cloud
  Agents) and version (Settings → About, or `ao --version`)
- your OS and how AO was installed
- steps to reproduce, or a proof of concept
- the impact you believe it has

## What to expect

- We acknowledge new reports within **3 business days**.
- We send an initial assessment, including whether we accept the report,
  within **10 business days**.
- We keep you updated while we work on a fix, and we coordinate the disclosure
  date with you. We aim to release a fix within **90 days** of the report.
- With your permission, we credit you in the published GitHub Security
  Advisory.

Please give us a reasonable chance to fix the problem before you disclose it
publicly, and avoid accessing or changing other people's data while you
investigate.

## Supported versions

Security fixes ship in the next stable release and in nightly builds. The app
updates itself, so the latest stable release is the supported version.

| Version | Supported |
| :-- | :-- |
| Latest stable release | Yes |
| Latest nightly build | Yes |
| Older releases | No. Please update. |
| Legacy npm package `@aoagents/ao` (frozen at `0.10.3`) | No |

## Scope

In scope: code in this repository, including the desktop app, the `ao` CLI and
daemon, the mobile companion app, and the published release artifacts.

The AO Cloud control plane is not in this repository. Report problems with it
through the same private form, and we will route them.

Problems in a third-party coding agent (Claude Code, Codex, and others) belong
with that agent's maintainers, unless Orchestrator.inc makes them worse.
