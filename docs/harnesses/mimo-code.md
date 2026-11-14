# MiMo Code Adapter

AO supports [MiMo Code](https://github.com/XiaomiMiMo/MiMo-Code) v0.1.14 or
newer as a terminal worker or orchestrator. The adapter uses the user-installed
`mimo` executable; AO does not bundle the provider.

## Install and authenticate

Install the official npm package:

```bash
npm install -g @mimo-ai/cli
mimo
```

MiMo Auto is available without login. To use paid or premium models, run
`mimo auth login` and choose MiMo browser login.
Other providers can be connected by API key through MiMo Code's `/connect`
flow; see the official [provider guide](https://github.com/XiaomiMiMo/MiMo-Code/blob/main/packages/opencode/src/skill/builtin/.bundle/mimocode-docs/reference/providers.md).

AO first checks credentials with `mimo auth list` and the free channel with
`mimo models mimo`. These local commands establish configuration, not provider
access. For Z.AI and Zhipu API or Coding Plan routes, AO resolves the configured
or recent model using MiMo's resolved config, native paths and available models,
then sends a bounded, one-token request to that model's endpoint. A recognized
completion marks authentication **authorized** and Harness Settings **Connected**.
This check can consume provider quota, including a metered prompt on Coding
Plans. It can run during background readiness checks after the normal readiness
cache expires, as well as explicit rechecks; it is not limited to user-initiated
login. There is no separate long-lived provider-verification cache.

A rejected key is reported as needing authentication. Network errors, rate
limits, unsupported providers, ambiguous defaults, custom model definitions and
external plugins stay **configured**. Inconclusive or unsupported provider-validation
diagnostics show **Credentials not verified** on the Harness page; other configured
states, including the credential-free MiMo Auto channel, use a neutral **Installed**
label. Credentials remain selectable without a verification badge in
agent dropdowns. AO does not start a native session or give the probe any tools.

## AO integration

- New tasks are delivered with `mimo --prompt`; model overrides use `--model`.
- AO passes `--trust` for its supervised session worktree.
- AO installs an observation-only hook at `.mimocode/hooks/ao-activity.ts` and
  the `using-ao` skill under `.mimocode/skills/`.
- Standing instructions use an AO-owned, session-specific custom agent under
  `.mimocode/agents/`; user agents and hooks are preserved.
- Native IDs beginning with `ses_` are captured from lifecycle events and
  restored exactly with `mimo --session <id>`.

Permission modes map to MiMo's native controls: default keeps configured
behavior, accept-edits allows edits only, and auto/bypass use MiMo's documented
`--dangerously-skip-permissions` mode. That native mode preserves explicit
denies and also opts into irreversible-delete approval; MiMo currently exposes
no separate startup switch for AO's narrower auto mode.

This integration is TUI-only. It does not register MiMo Code for Chat,
reviewers, interface handoff, or first-class nested-agent sessions.
