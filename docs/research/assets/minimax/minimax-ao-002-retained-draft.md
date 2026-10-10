# MiniMax AO-002 preserved failure

This diagnostic run is finalized FAIL. Native and AO initial-task evidence passed, but the cancellation settle observation failed and restored empty-input readiness was blocked. Later fixes/controls do not replace this result.

| Gate | Result | Evidence |
| --- | --- | --- |
| local_binary | PASS | local executable resolved |
| local_version | PASS | local version command succeeded |
| local_integration | PASS | local integration probe succeeded |
| local_session_spawn | PASS | direct local CLI session produced the requested proof |
| local_models_list | PASS | local CLI listed 1 model(s) |
| registered | PASS | agent is registered |
| installed | PASS | AO resolved the installed executable |
| fresh_probe | PASS | installation and authentication observations are fresh |
| authentication | BLOCKED | MiniMax Code has credentials configured, but AO could not verify them. |
| ao_models_api | PASS | AO model API listed 1 model(s) |
| tui_spawn | PASS | AO created the requested TUI session |
| proof_file_creation | PASS | AO_AUDIT_PROOF.json contains valid JSON |
| correct_working_directory | PASS | provider reported AO's session workspace |
| initial_prompt_exactly_once | PASS | unique initial token was handled once |
| hidden_ao_instructions | PASS | provider consumed hidden AO standing instructions |
| project_agents_md | PASS | provider consumed project AGENTS.md |
| activity_status | PASS | AO exposed ready status and an active→idle transition |
| second_message | PASS | second API message produced the requested provider-authored mutation |
| native_session_id | PASS | AO persisted a provider-native session identity |
| cancellation | FAIL | the same active TUI session did not settle after mux input |
| termination | PASS | kill was acknowledged and termination was confirmed |
| native_restore | PASS | AO restored in native mode with the exact provider-native ID |
| same_ao_session_workspace | PASS | restore retained the AO session ID and workspace |
| post_restore_terminal_ready | BLOCKED | the restored native terminal ready cue was not observed before timeout |
| post_restore_message | NOT_RUN | restored native terminal readiness was not established |
| history_file_continuity | NOT_RUN | restored native terminal readiness was not established |
| system_prompt_restore | NOT_RUN | restored native terminal readiness was not established |
| model_catalog_consistency | PASS | 1 model ID(s) are shared by local CLI and AO |

The real AO screenshot was captured later from the same restored session at 2026-10-10T13:27:03Z. It visibly includes the successful initial file creation and second edit, plus the retained cancellation draft. This is not a successful post-restore continuation.

Capture record: minimax-ao-002-retained-draft.provenance.json. Image: minimax-ao-002-retained-draft.png.

The configured authentication state remains BLOCKED in the strict audit even though the authorized native provider call succeeded. This report does not relabel stored credentials as verified authentication.
