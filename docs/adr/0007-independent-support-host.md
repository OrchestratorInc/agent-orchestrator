# Desktop problem reporting without a working daemon

Status: proposed

Report a Problem starts bounded local diagnostic collection while the user chooses a configured harness and model. The desktop opens an ordinary standalone session when its local daemon is healthy. It attaches a private snapshot and the bundled bug-triage playbook, so the user does not write a diagnostic prompt.

The desktop freezes the request, attachment bytes and client request ID before recording POST intent. Retries reuse that request. A failed or unknown POST never starts a second fallback agent. A linked ordinary session belongs to the existing session lifecycle.

If the daemon cannot start, the hidden `ao support-host` process collects local evidence and launches the chosen native agent independently. Chat-capable agents use existing Chat drivers and shared conversation controls. Other agents run in a long-lived native terminal through the registered launch adapter, PTY attach stream and existing terminal component. Input, resize, approvals and model behavior stay native. The helper never interprets assistant prose as publication instructions or screen output as a form.

Collection reads bounded local build, configuration-path, runfile, supervisor, machine, log and retained session facts. SQLite opens read-only without migrations. Missing or unreadable sources produce partial evidence. Private reports stay under the resolved AO data directory. Live native requests and terminal replay are not persisted in report manifests. Relevant registered project paths are private operational context for the agent's normal read tools.

The agent follows the shared bug-triage skill. It asks short questions when the user's observation is unclear, searches open and closed issues and pull requests, and returns an existing match or files a concise issue using its existing GitHub tools. Starting the investigation authorizes filing. Full logs, transcripts and private files are not public attachments by default.

Offline Chat accepts text and native approval or input requests. File attachments require a normal daemon session; the helper rejects them and keeps the message in the composer.

GitHub authentication, publication and uncertain-write reconciliation belong to the selected agent and its tools. AO does not implement a second publisher or promise an enforced, exactly-once GitHub write. A local-draft instruction is not a security boundary for an agent with unrestricted native tools. Tests use fake providers and transports rather than live authenticated report agents.

This is an internal desktop helper, not a new installation path, listener or daemon API. It does not start, repair or restart the daemon. Reports remain accessible when the daemon fails. A helper interrupted before it saves a launch payload requires fresh preparation. A terminated native process requires a new investigation rather than transparent conversation recovery.
