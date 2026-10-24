# Cortex transport gate recovery — blocked

This is native artifact/launch evidence for PR5507, not an AO integration pass. The old `1.1.87` fixture remains historical SDK stream-json flag evidence. Its missing `--permission-prompt-tool` does **not** reject native ACP. The official SDK's additive `appendSystemPrompt` control-initialize request is also separate from ACP; no inferred ACP metadata or replace-only prompt is permitted.

The manifest-pinned stable `1.1.104+041358.66de1c44636f` archive matched SHA-256 on both streamed reads. The stock executable plus bundled node_modules reports `Cortex Code v0.26.1012`. The initial executable-only attempt failed due to missing bundled `fs-native-extensions`; that failure is retained. No patched native release was used. Optional bundled Python runtimes were not extracted because of disk constraints.

Actual `acp serve --help` exposes connection, model, workdir and repeatable plugin-dir. The new executable gate checks this launch surface independently of SDK flags. Passing it proves no authentication, injection, role, permissions or lifecycle capability.

With documented isolated empty `--config`/`--config-file`, an explicit nonexistent connection, audit workspace and noninteractive stdin, startup exited 1 before handshake: onboarding requires an interactive terminal. No configured Snowflake connection is available; no onboarding, signup, alternative vendor key or provider turn was attempted. Native help itself created default native metadata/log files under `~/.snowflake/cortex`; these are preserved, and no user/project instruction or credential files were changed.

| Gate | Result |
| --- | --- |
| Released archive hash / exact executable version / ACP help | Observed |
| ACP initialize / new / load / replay | NOT_RUN: onboarding and Snowflake auth prerequisite |
| Private fresh/restore token consumption | NOT_RUN |
| Additive provider defaults and project/user rule preservation | NOT_RUN |
| Actual provider message role | UNKNOWN |
| Permission approve/reject/cancel; child reaping | NOT_RUN |
| Registered AO session / actual AO screenshot | NOT_RUN; screenshot unavailable without admitted authenticated surface |

No adapter, registry, migration, UI label or capability declaration is added. Continue this existing PR after an authorized Snowflake connection is available: prove ACP plugin SessionStart context consumption/role on begin and exact load, or separately qualify the documented SDK additive transport; then use existing nativeacp/persistent-host boundaries and capture actual AO lifecycle/UI proof. Native configuration/help cannot substitute for consumption.

Routine tests were added but not run on the VPS. Current-head GitHub CI owns validation. All detailed evidence belongs here on PR5507, never in skillPR6509.

Sources: [ACP](https://docs.snowflake.com/en/user-guide/cortex-code/cortex-code-acp), [SDK system prompts](https://docs.snowflake.com/en/user-guide/cortex-code-agent-sdk/system-prompts), [manifest](https://sfc-repo.snowflakecomputing.com/cortex-code-cli/a4643c4278/1.1.104%2B041358.66de1c44636f/manifest.json). Help paths are redacted as `<audit-checkout>`; failure paths as `<audit-native>`.
