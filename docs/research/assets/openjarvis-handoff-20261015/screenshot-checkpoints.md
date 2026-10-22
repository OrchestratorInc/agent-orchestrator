# OpenJarvis ordered screenshot checkpoints — final disposition

Initial index was created before runtime attempt001 and before any UI driving; original planning bytes retained in screenshot-checkpoints-planned.md. No UI was ever driven. No real AO image exists; screenshot tool scrot was reviewed but not invoked without an owned Electron window.

Shared provenance: runtime77574094 / daemon SHA25635b412ab9855f4cf076a734c1d251bfa3a28993e585fc1e9103fe192ea26cbc4; edited17368d35 NOT_RUN; Linux VPS. UI source/capture time/image SHA/attachment: unavailable because no Electron launch/capture. Ports18437/18438; exact identities in final-session-identities.json. No session exists for prelaunch checkpoints; those use harness openjarvis/selected scratch host.

|Checkpoint|Action → expected/observed|Functional UI status|Screenshot/attachment|Auxiliary proof or reason|
|---|---|---|---|---|
|harness-listed|Open Agent Harness → OpenJarvis row/name/logo/install/auth|NOT_RUN|unavailable|API registry and fresh probe installed/configured; actual Electron unavailable|
|install-offer|Open row install method → official manual guidance|NOT_RUN|unavailable|MANUAL_ONLY; attempt002 installer API available=false automatic=false; actual guide image unavailable|
|install-run|Start install → progress/result|NOT_RUN|unavailable|MANUAL_ONLY existing binary; no in-page install or dependency installation attempted|
|auth-before|Open existing configured state → safe account status|NOT_RUN|unavailable|Probe200 configured plus successful provider turn; actual UI state absent|
|auth-action|Use setup affordance → guide/result|NOT_RUN|unavailable|MANUAL_ONLY; native setup documentation plan, existing authorized route used; no UI action|
|auth-ready|Refresh auth → actual ready/configured state|NOT_RUN|unavailable|Fresh probe configured; successful specific provider route, not fresh-login proof|
|harness-models|Open/refresh Agent Harness model surface → result|NOT_RUN|unavailable|No UI; implementation presence not visually established; API text/manual does not qualify this panel|
|task-agent|Open New task and select OpenJarvis → selection|NOT_RUN|unavailable|API-created worker is not New Task UI evidence|
|task-model|Open model input and choose ID → saved selection|NOT_RUN|unavailable|API manual text mode; configured effective model and explicit API override glm-5.3-flash observed; UI absent|
|task-running|Create task → running Kanban and settled TUI/file effect|NOT_RUN|unavailable|API/native ordinary.txt hello and follow-up observed only; no card/TUI AO capture|
|task-followup|Native composer submit → result|NOT_RUN|unavailable|API follow-up appended world; post-cancel API send unconfirmed FAIL; actual composer UI not exercised|
|task-cancel|Executing child → Ctrl+C → settled native frame|NOT_RUN|unavailable|Native host control reaped witnessed child/no late effect in attempt001; no actual AO viewport screenshot|
|kill-action|Archive control/confirmation → terminated|NOT_RUN|unavailable|API Kill executing child PASS/no late/control isolation in attempt002; actual UI action/result absent|
|archived|Open Archive → exact entry|NOT_RUN|unavailable|Same terminated AO records observed via API/SQLite; no archived entry screenshot|
|restore-action|Restore exact archived conversation → refusal|N/A|unavailable|Native history in memory; HTTP500 actual refusal keeps terminated. Local typed409 proposed only; no silent fresh replacement|
|kanban-return|Return same restored session → continuation|N/A|unavailable|Exact native restore unsupported; no replacement claimed|
|chat|Choose Chat → truthful unsupported refusal|N/A|unavailable|Actual registered API409 SESSION_MODE_UNSUPPORTED; no Chat driver, no UI Chat claim|
|reconnect-cleanup|Reconnect/continue then final cleanup → no owned runtime|NOT_RUN|unavailable|Reconnect NOT_RUN; four exact records terminated/two daemons stopped/ports closed. No UI teardown screenshot|

All applicable UI checkpoints stay NOT_RUN and aggregate AO UI evidence is BLOCKED. API/native functional observations are explicitly auxiliary; they do not replace screenshots. The manual install/config branches were not promoted to successful in-page flows. Restore/Chat N/A describes the agreed native limitation with actual API refusals, not a passing restored journey.
