# M3 变更文件与测试规模

按 entry-files.json 实算 SHA256；包含入场后修改及新增文件，不把已有未修改的 M2 文件算成 M3 新增。

| 文件 | 入场后变化 | 当前行数 |
|---|---|---:|
| [backend/internal/adapters/chatdriver/processenv/processenv.go](E:/code/agent-orchestrator/backend/internal/adapters/chatdriver/processenv/processenv.go) | modified | 70 |
| [backend/internal/adapters/chatdriver/processenv/processenv_test.go](E:/code/agent-orchestrator/backend/internal/adapters/chatdriver/processenv/processenv_test.go) | modified | 80 |
| [backend/internal/adapters/resources/sampler.go](E:/code/agent-orchestrator/backend/internal/adapters/resources/sampler.go) | new | 71 |
| [backend/internal/adapters/resources/sampler_test.go](E:/code/agent-orchestrator/backend/internal/adapters/resources/sampler_test.go) | new | 80 |
| [backend/internal/adapters/runtime/conpty/host_conpty_darwin.go](E:/code/agent-orchestrator/backend/internal/adapters/runtime/conpty/host_conpty_darwin.go) | modified | 147 |
| [backend/internal/adapters/runtime/conpty/host_conpty_linux.go](E:/code/agent-orchestrator/backend/internal/adapters/runtime/conpty/host_conpty_linux.go) | modified | 356 |
| [backend/internal/adapters/runtime/conpty/host_conpty_windows.go](E:/code/agent-orchestrator/backend/internal/adapters/runtime/conpty/host_conpty_windows.go) | modified | 137 |
| [backend/internal/adapters/runtime/conpty/spawn.go](E:/code/agent-orchestrator/backend/internal/adapters/runtime/conpty/spawn.go) | modified | 178 |
| [backend/internal/adapters/runtime/conpty/workflow_env_test.go](E:/code/agent-orchestrator/backend/internal/adapters/runtime/conpty/workflow_env_test.go) | new | 62 |
| [backend/internal/adapters/runtime/tmux/tmux.go](E:/code/agent-orchestrator/backend/internal/adapters/runtime/tmux/tmux.go) | modified | 1761 |
| [backend/internal/adapters/runtime/tmux/workflow_env_test.go](E:/code/agent-orchestrator/backend/internal/adapters/runtime/tmux/workflow_env_test.go) | new | 54 |
| [backend/internal/cdc/event.go](E:/code/agent-orchestrator/backend/internal/cdc/event.go) | modified | 58 |
| [backend/internal/cli/mcp_workflow.go](E:/code/agent-orchestrator/backend/internal/cli/mcp_workflow.go) | modified | 606 |
| [backend/internal/cli/mcp_workflow_credentials_test.go](E:/code/agent-orchestrator/backend/internal/cli/mcp_workflow_credentials_test.go) | new | 42 |
| [backend/internal/cli/mcp_workflow_test.go](E:/code/agent-orchestrator/backend/internal/cli/mcp_workflow_test.go) | modified | 443 |
| [backend/internal/daemon/automation_wiring.go](E:/code/agent-orchestrator/backend/internal/daemon/automation_wiring.go) | modified | 32 |
| [backend/internal/daemon/daemon.go](E:/code/agent-orchestrator/backend/internal/daemon/daemon.go) | modified | 1734 |
| [backend/internal/daemon/execution_recovery_test.go](E:/code/agent-orchestrator/backend/internal/daemon/execution_recovery_test.go) | modified | 818 |
| [backend/internal/daemon/execution_wiring.go](E:/code/agent-orchestrator/backend/internal/daemon/execution_wiring.go) | modified | 295 |
| [backend/internal/daemon/m2_wiring_test.go](E:/code/agent-orchestrator/backend/internal/daemon/m2_wiring_test.go) | modified | 895 |
| [backend/internal/daemon/m3_execution_fixture_test.go](E:/code/agent-orchestrator/backend/internal/daemon/m3_execution_fixture_test.go) | new | 43 |
| [backend/internal/daemon/scheduler_runtime.go](E:/code/agent-orchestrator/backend/internal/daemon/scheduler_runtime.go) | new | 166 |
| [backend/internal/daemon/scheduler_wiring.go](E:/code/agent-orchestrator/backend/internal/daemon/scheduler_wiring.go) | new | 160 |
| [backend/internal/daemon/wiring_test.go](E:/code/agent-orchestrator/backend/internal/daemon/wiring_test.go) | modified | 1215 |
| [backend/internal/daemon/workflow_wiring.go](E:/code/agent-orchestrator/backend/internal/daemon/workflow_wiring.go) | modified | 86 |
| [backend/internal/domain/budget.go](E:/code/agent-orchestrator/backend/internal/domain/budget.go) | new | 85 |
| [backend/internal/domain/budget_snapshot.go](E:/code/agent-orchestrator/backend/internal/domain/budget_snapshot.go) | new | 87 |
| [backend/internal/domain/budget_snapshot_test.go](E:/code/agent-orchestrator/backend/internal/domain/budget_snapshot_test.go) | new | 39 |
| [backend/internal/domain/budget_usage.go](E:/code/agent-orchestrator/backend/internal/domain/budget_usage.go) | new | 54 |
| [backend/internal/domain/execution_permit.go](E:/code/agent-orchestrator/backend/internal/domain/execution_permit.go) | new | 51 |
| [backend/internal/domain/scheduler.go](E:/code/agent-orchestrator/backend/internal/domain/scheduler.go) | new | 191 |
| [backend/internal/domain/workflow_materialization.go](E:/code/agent-orchestrator/backend/internal/domain/workflow_materialization.go) | new | 199 |
| [backend/internal/domain/workflow_materialization_test.go](E:/code/agent-orchestrator/backend/internal/domain/workflow_materialization_test.go) | new | 95 |
| [backend/internal/httpd/api.go](E:/code/agent-orchestrator/backend/internal/httpd/api.go) | modified | 532 |
| [backend/internal/httpd/apispec/openapi.yaml](E:/code/agent-orchestrator/backend/internal/httpd/apispec/openapi.yaml) | modified | 27883 |
| [backend/internal/httpd/apispec/specgen/build.go](E:/code/agent-orchestrator/backend/internal/httpd/apispec/specgen/build.go) | modified | 4272 |
| [backend/internal/httpd/controllers/dto.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/dto.go) | modified | 4810 |
| [backend/internal/httpd/controllers/operator_approvals.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/operator_approvals.go) | modified | 268 |
| [backend/internal/httpd/controllers/operator_approvals_fixture_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/operator_approvals_fixture_test.go) | modified | 411 |
| [backend/internal/httpd/controllers/operator_approvals_m2_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/operator_approvals_m2_test.go) | modified | 152 |
| [backend/internal/httpd/controllers/operator_approvals_refusal_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/operator_approvals_refusal_test.go) | modified | 162 |
| [backend/internal/httpd/controllers/operator_approvals_security_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/operator_approvals_security_test.go) | modified | 251 |
| [backend/internal/httpd/controllers/scheduler.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/scheduler.go) | new | 299 |
| [backend/internal/httpd/controllers/scheduler_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/scheduler_test.go) | new | 105 |
| [backend/internal/httpd/controllers/workflows.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/workflows.go) | modified | 2616 |
| [backend/internal/httpd/controllers/workflows_m2_authz_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/workflows_m2_authz_test.go) | modified | 862 |
| [backend/internal/httpd/controllers/workflows_m2_bind_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/workflows_m2_bind_test.go) | modified | 1001 |
| [backend/internal/httpd/controllers/workflows_m2_identity_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/workflows_m2_identity_test.go) | modified | 880 |
| [backend/internal/httpd/controllers/workflows_m3_fixture_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/workflows_m3_fixture_test.go) | new | 123 |
| [backend/internal/httpd/controllers/workflows_m3_materialization_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/workflows_m3_materialization_test.go) | new | 85 |
| [backend/internal/httpd/controllers/workflows_m3_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/workflows_m3_test.go) | new | 67 |
| [backend/internal/httpd/controllers/workflows_test.go](E:/code/agent-orchestrator/backend/internal/httpd/controllers/workflows_test.go) | modified | 877 |
| [backend/internal/integration/m2_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m2_e2e_test.go) | modified | 1725 |
| [backend/internal/integration/m2_formal_cli_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m2_formal_cli_e2e_test.go) | modified | 213 |
| [backend/internal/integration/m2_full_flow_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m2_full_flow_e2e_test.go) | modified | 1790 |
| [backend/internal/integration/m2_mcp_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m2_mcp_e2e_test.go) | modified | 464 |
| [backend/internal/integration/m2_session_manager_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m2_session_manager_e2e_test.go) | modified | 1261 |
| [backend/internal/integration/m3_admission_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m3_admission_e2e_test.go) | new | 73 |
| [backend/internal/integration/m3_budget_resources_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m3_budget_resources_e2e_test.go) | new | 155 |
| [backend/internal/integration/m3_compat_fixture_test.go](E:/code/agent-orchestrator/backend/internal/integration/m3_compat_fixture_test.go) | new | 66 |
| [backend/internal/integration/m3_fixture_test.go](E:/code/agent-orchestrator/backend/internal/integration/m3_fixture_test.go) | new | 226 |
| [backend/internal/integration/m3_materialization_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m3_materialization_e2e_test.go) | new | 69 |
| [backend/internal/integration/m3_process_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m3_process_e2e_test.go) | new | 201 |
| [backend/internal/integration/m3_recovery_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m3_recovery_e2e_test.go) | new | 100 |
| [backend/internal/integration/m3_scheduler_e2e_test.go](E:/code/agent-orchestrator/backend/internal/integration/m3_scheduler_e2e_test.go) | new | 112 |
| [backend/internal/ports/execution.go](E:/code/agent-orchestrator/backend/internal/ports/execution.go) | modified | 548 |
| [backend/internal/ports/scheduler.go](E:/code/agent-orchestrator/backend/internal/ports/scheduler.go) | new | 46 |
| [backend/internal/service/budget/control.go](E:/code/agent-orchestrator/backend/internal/service/budget/control.go) | new | 33 |
| [backend/internal/service/budget/service.go](E:/code/agent-orchestrator/backend/internal/service/budget/service.go) | new | 70 |
| [backend/internal/service/chat/service.go](E:/code/agent-orchestrator/backend/internal/service/chat/service.go) | modified | 3324 |
| [backend/internal/service/execution/admission_recovery_test.go](E:/code/agent-orchestrator/backend/internal/service/execution/admission_recovery_test.go) | new | 96 |
| [backend/internal/service/execution/coordinator.go](E:/code/agent-orchestrator/backend/internal/service/execution/coordinator.go) | modified | 1510 |
| [backend/internal/service/execution/coordinator_credentials_test.go](E:/code/agent-orchestrator/backend/internal/service/execution/coordinator_credentials_test.go) | new | 54 |
| [backend/internal/service/execution/coordinator_test.go](E:/code/agent-orchestrator/backend/internal/service/execution/coordinator_test.go) | modified | 2246 |
| [backend/internal/service/execution/local_gateway.go](E:/code/agent-orchestrator/backend/internal/service/execution/local_gateway.go) | modified | 1096 |
| [backend/internal/service/execution/ownership.go](E:/code/agent-orchestrator/backend/internal/service/execution/ownership.go) | modified | 279 |
| [backend/internal/service/execution/ownership_admission_test.go](E:/code/agent-orchestrator/backend/internal/service/execution/ownership_admission_test.go) | new | 36 |
| [backend/internal/service/execution/permit.go](E:/code/agent-orchestrator/backend/internal/service/execution/permit.go) | new | 97 |
| [backend/internal/service/execution/reanchor_permit_fixture_test.go](E:/code/agent-orchestrator/backend/internal/service/execution/reanchor_permit_fixture_test.go) | new | 24 |
| [backend/internal/service/execution/reanchor_test.go](E:/code/agent-orchestrator/backend/internal/service/execution/reanchor_test.go) | modified | 729 |
| [backend/internal/service/execution/recovery.go](E:/code/agent-orchestrator/backend/internal/service/execution/recovery.go) | new | 199 |
| [backend/internal/service/principal/m3_identity_fixture_test.go](E:/code/agent-orchestrator/backend/internal/service/principal/m3_identity_fixture_test.go) | new | 61 |
| [backend/internal/service/principal/m3_identity_test.go](E:/code/agent-orchestrator/backend/internal/service/principal/m3_identity_test.go) | new | 173 |
| [backend/internal/service/principal/operator_approval.go](E:/code/agent-orchestrator/backend/internal/service/principal/operator_approval.go) | modified | 819 |
| [backend/internal/service/principal/operator_approval_test.go](E:/code/agent-orchestrator/backend/internal/service/principal/operator_approval_test.go) | modified | 207 |
| [backend/internal/service/principal/operator_approval_trust_test.go](E:/code/agent-orchestrator/backend/internal/service/principal/operator_approval_trust_test.go) | modified | 212 |
| [backend/internal/service/principal/principal.go](E:/code/agent-orchestrator/backend/internal/service/principal/principal.go) | modified | 316 |
| [backend/internal/service/principal/principal_test.go](E:/code/agent-orchestrator/backend/internal/service/principal/principal_test.go) | modified | 734 |
| [backend/internal/service/principal/verifier.go](E:/code/agent-orchestrator/backend/internal/service/principal/verifier.go) | modified | 203 |
| [backend/internal/service/scheduler/admission_test.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/admission_test.go) | new | 222 |
| [backend/internal/service/scheduler/candidate_priority_test.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/candidate_priority_test.go) | new | 41 |
| [backend/internal/service/scheduler/control.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/control.go) | new | 104 |
| [backend/internal/service/scheduler/fairness_test.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/fairness_test.go) | new | 209 |
| [backend/internal/service/scheduler/fixture_test.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/fixture_test.go) | new | 187 |
| [backend/internal/service/scheduler/leadership_concurrency_test.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/leadership_concurrency_test.go) | new | 46 |
| [backend/internal/service/scheduler/leadership_cursor_test.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/leadership_cursor_test.go) | new | 103 |
| [backend/internal/service/scheduler/planner.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/planner.go) | new | 256 |
| [backend/internal/service/scheduler/resources.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/resources.go) | new | 121 |
| [backend/internal/service/scheduler/resources_test.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/resources_test.go) | new | 90 |
| [backend/internal/service/scheduler/retry.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/retry.go) | new | 94 |
| [backend/internal/service/scheduler/retry_test.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/retry_test.go) | new | 164 |
| [backend/internal/service/scheduler/service.go](E:/code/agent-orchestrator/backend/internal/service/scheduler/service.go) | new | 285 |
| [backend/internal/service/workflow/admission_test.go](E:/code/agent-orchestrator/backend/internal/service/workflow/admission_test.go) | new | 43 |
| [backend/internal/service/workflow/materialization.go](E:/code/agent-orchestrator/backend/internal/service/workflow/materialization.go) | new | 61 |
| [backend/internal/service/workflow/materialization_test.go](E:/code/agent-orchestrator/backend/internal/service/workflow/materialization_test.go) | new | 52 |
| [backend/internal/service/workflow/service.go](E:/code/agent-orchestrator/backend/internal/service/workflow/service.go) | modified | 873 |
| [backend/internal/service/workflow/service_test.go](E:/code/agent-orchestrator/backend/internal/service/workflow/service_test.go) | modified | 659 |
| [backend/internal/session_manager/admission.go](E:/code/agent-orchestrator/backend/internal/session_manager/admission.go) | new | 85 |
| [backend/internal/session_manager/admission_fixture_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/admission_fixture_test.go) | new | 36 |
| [backend/internal/session_manager/admission_reattach.go](E:/code/agent-orchestrator/backend/internal/session_manager/admission_reattach.go) | new | 61 |
| [backend/internal/session_manager/admission_restart.go](E:/code/agent-orchestrator/backend/internal/session_manager/admission_restart.go) | new | 51 |
| [backend/internal/session_manager/admission_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/admission_test.go) | new | 129 |
| [backend/internal/session_manager/chat_resume_unpersisted_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/chat_resume_unpersisted_test.go) | modified | 138 |
| [backend/internal/session_manager/chat_spawn.go](E:/code/agent-orchestrator/backend/internal/session_manager/chat_spawn.go) | modified | 843 |
| [backend/internal/session_manager/chat_spawn_async_drain_integration_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/chat_spawn_async_drain_integration_test.go) | modified | 217 |
| [backend/internal/session_manager/chat_spawn_workflow.go](E:/code/agent-orchestrator/backend/internal/session_manager/chat_spawn_workflow.go) | modified | 64 |
| [backend/internal/session_manager/chat_spawn_workflow_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/chat_spawn_workflow_test.go) | new | 105 |
| [backend/internal/session_manager/f03_binding_worktrees_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/f03_binding_worktrees_test.go) | modified | 184 |
| [backend/internal/session_manager/hibernated_chat_nudge_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/hibernated_chat_nudge_test.go) | modified | 209 |
| [backend/internal/session_manager/interface_transition_chat_never_started_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/interface_transition_chat_never_started_test.go) | modified | 201 |
| [backend/internal/session_manager/interface_transition_history_integration_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/interface_transition_history_integration_test.go) | modified | 352 |
| [backend/internal/session_manager/interface_transition_rollback_chat_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/interface_transition_rollback_chat_test.go) | modified | 125 |
| [backend/internal/session_manager/interface_transition_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/interface_transition_test.go) | modified | 3390 |
| [backend/internal/session_manager/manager.go](E:/code/agent-orchestrator/backend/internal/session_manager/manager.go) | modified | 7460 |
| [backend/internal/session_manager/model_restore_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/model_restore_test.go) | modified | 233 |
| [backend/internal/session_manager/multi_repo_workspace_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/multi_repo_workspace_test.go) | modified | 439 |
| [backend/internal/session_manager/startup_health.go](E:/code/agent-orchestrator/backend/internal/session_manager/startup_health.go) | modified | 199 |
| [backend/internal/session_manager/startup_reattach_fixture_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/startup_reattach_fixture_test.go) | new | 236 |
| [backend/internal/session_manager/startup_reattach_legacy_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/startup_reattach_legacy_test.go) | new | 80 |
| [backend/internal/session_manager/startup_reattach_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/startup_reattach_test.go) | new | 211 |
| [backend/internal/session_manager/update_when_idle_reaper_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/update_when_idle_reaper_test.go) | modified | 102 |
| [backend/internal/session_manager/workflow_context.go](E:/code/agent-orchestrator/backend/internal/session_manager/workflow_context.go) | modified | 112 |
| [backend/internal/session_manager/workflow_context_security_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/workflow_context_security_test.go) | new | 45 |
| [backend/internal/session_manager/workflow_context_test.go](E:/code/agent-orchestrator/backend/internal/session_manager/workflow_context_test.go) | modified | 246 |
| [backend/internal/storage/sqlite/gen/budget_usage.sql.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/gen/budget_usage.sql.go) | new | 320 |
| [backend/internal/storage/sqlite/gen/execution_permit.sql.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/gen/execution_permit.sql.go) | new | 325 |
| [backend/internal/storage/sqlite/gen/models.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/gen/models.go) | modified | 1414 |
| [backend/internal/storage/sqlite/gen/scheduler.sql.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/gen/scheduler.sql.go) | new | 1085 |
| [backend/internal/storage/sqlite/gen/workflow_materialization.sql.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/gen/workflow_materialization.sql.go) | new | 194 |
| [backend/internal/storage/sqlite/migrate_burned_versions_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/migrate_burned_versions_test.go) | modified | 502 |
| [backend/internal/storage/sqlite/migrate_execution_generation_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/migrate_execution_generation_test.go) | new | 56 |
| [backend/internal/storage/sqlite/migrate_scheduler_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/migrate_scheduler_test.go) | new | 35 |
| [backend/internal/storage/sqlite/migrate_workflow_collaboration_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/migrate_workflow_collaboration_test.go) | modified | 399 |
| [backend/internal/storage/sqlite/migrations/20261105073957583_m3_scheduler_admission.sql](E:/code/agent-orchestrator/backend/internal/storage/sqlite/migrations/20261105073957583_m3_scheduler_admission.sql) | new | 123 |
| [backend/internal/storage/sqlite/migrations/20261105084719358_m3_task_request_materialization.sql](E:/code/agent-orchestrator/backend/internal/storage/sqlite/migrations/20261105084719358_m3_task_request_materialization.sql) | new | 30 |
| [backend/internal/storage/sqlite/migrations/20261105090651703_m3_budget_ledger.sql](E:/code/agent-orchestrator/backend/internal/storage/sqlite/migrations/20261105090651703_m3_budget_ledger.sql) | new | 66 |
| [backend/internal/storage/sqlite/migrations/20261105094026402_m3_scheduler_control.sql](E:/code/agent-orchestrator/backend/internal/storage/sqlite/migrations/20261105094026402_m3_scheduler_control.sql) | new | 17 |
| [backend/internal/storage/sqlite/migrations/20261105101553243_m3_session_execution_generations.sql](E:/code/agent-orchestrator/backend/internal/storage/sqlite/migrations/20261105101553243_m3_session_execution_generations.sql) | new | 79 |
| [backend/internal/storage/sqlite/migrations/20261105102052187_m3_budget_session_attribution.sql](E:/code/agent-orchestrator/backend/internal/storage/sqlite/migrations/20261105102052187_m3_budget_session_attribution.sql) | new | 57 |
| [backend/internal/storage/sqlite/queries/budget_usage.sql](E:/code/agent-orchestrator/backend/internal/storage/sqlite/queries/budget_usage.sql) | new | 54 |
| [backend/internal/storage/sqlite/queries/execution_permit.sql](E:/code/agent-orchestrator/backend/internal/storage/sqlite/queries/execution_permit.sql) | new | 61 |
| [backend/internal/storage/sqlite/queries/scheduler.sql](E:/code/agent-orchestrator/backend/internal/storage/sqlite/queries/scheduler.sql) | new | 154 |
| [backend/internal/storage/sqlite/queries/workflow_materialization.sql](E:/code/agent-orchestrator/backend/internal/storage/sqlite/queries/workflow_materialization.sql) | new | 31 |
| [backend/internal/storage/sqlite/sqlitetest/admission.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest/admission.go) | new | 132 |
| [backend/internal/storage/sqlite/sqlitetest/execution.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest/execution.go) | new | 30 |
| [backend/internal/storage/sqlite/store/budget_correction_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/budget_correction_test.go) | new | 75 |
| [backend/internal/storage/sqlite/store/budget_restart_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/budget_restart_test.go) | new | 73 |
| [backend/internal/storage/sqlite/store/budget_settlement_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/budget_settlement_test.go) | new | 105 |
| [backend/internal/storage/sqlite/store/budget_usage_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/budget_usage_store.go) | new | 212 |
| [backend/internal/storage/sqlite/store/budget_usage_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/budget_usage_test.go) | new | 147 |
| [backend/internal/storage/sqlite/store/execution_coordinator_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/execution_coordinator_test.go) | modified | 392 |
| [backend/internal/storage/sqlite/store/execution_permit_fixture_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/execution_permit_fixture_test.go) | new | 85 |
| [backend/internal/storage/sqlite/store/execution_permit_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/execution_permit_store.go) | new | 183 |
| [backend/internal/storage/sqlite/store/execution_permit_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/execution_permit_test.go) | new | 169 |
| [backend/internal/storage/sqlite/store/execution_reattach_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/execution_reattach_store.go) | new | 197 |
| [backend/internal/storage/sqlite/store/execution_restart_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/execution_restart_store.go) | new | 157 |
| [backend/internal/storage/sqlite/store/execution_restart_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/execution_restart_test.go) | new | 129 |
| [backend/internal/storage/sqlite/store/execution_session_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/execution_session_store.go) | new | 147 |
| [backend/internal/storage/sqlite/store/execution_settlement_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/execution_settlement_store.go) | new | 52 |
| [backend/internal/storage/sqlite/store/execution_settlement_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/execution_settlement_test.go) | new | 46 |
| [backend/internal/storage/sqlite/store/harness_release_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/harness_release_store.go) | modified | 105 |
| [backend/internal/storage/sqlite/store/m2_anomaly_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/m2_anomaly_test.go) | modified | 715 |
| [backend/internal/storage/sqlite/store/m2_e2e_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/m2_e2e_test.go) | modified | 469 |
| [backend/internal/storage/sqlite/store/m2_offline_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/m2_offline_test.go) | modified | 135 |
| [backend/internal/storage/sqlite/store/scheduler_admission_checks.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_admission_checks.go) | new | 178 |
| [backend/internal/storage/sqlite/store/scheduler_admission_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_admission_store.go) | new | 275 |
| [backend/internal/storage/sqlite/store/scheduler_admission_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_admission_test.go) | new | 196 |
| [backend/internal/storage/sqlite/store/scheduler_budget_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_budget_test.go) | new | 136 |
| [backend/internal/storage/sqlite/store/scheduler_candidate_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_candidate_store.go) | new | 42 |
| [backend/internal/storage/sqlite/store/scheduler_cdc_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_cdc_test.go) | new | 39 |
| [backend/internal/storage/sqlite/store/scheduler_control_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_control_store.go) | new | 78 |
| [backend/internal/storage/sqlite/store/scheduler_control_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_control_test.go) | new | 67 |
| [backend/internal/storage/sqlite/store/scheduler_leader_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_leader_store.go) | new | 107 |
| [backend/internal/storage/sqlite/store/scheduler_policy_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_policy_store.go) | new | 187 |
| [backend/internal/storage/sqlite/store/scheduler_recovery_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_recovery_test.go) | new | 127 |
| [backend/internal/storage/sqlite/store/scheduler_state_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_state_store.go) | new | 143 |
| [backend/internal/storage/sqlite/store/scheduler_upgrade_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/scheduler_upgrade_test.go) | new | 90 |
| [backend/internal/storage/sqlite/store/session_interface_transition_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/session_interface_transition_store.go) | modified | 468 |
| [backend/internal/storage/sqlite/store/session_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/session_store.go) | modified | 1474 |
| [backend/internal/storage/sqlite/store/workflow_admission_fixture_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_admission_fixture_test.go) | new | 18 |
| [backend/internal/storage/sqlite/store/workflow_binding_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_binding_store.go) | modified | 706 |
| [backend/internal/storage/sqlite/store/workflow_binding_store_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_binding_store_test.go) | modified | 1273 |
| [backend/internal/storage/sqlite/store/workflow_cdc_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_cdc_test.go) | modified | 427 |
| [backend/internal/storage/sqlite/store/workflow_collaboration_fixture_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_collaboration_fixture_test.go) | new | 59 |
| [backend/internal/storage/sqlite/store/workflow_collaboration_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_collaboration_store.go) | modified | 517 |
| [backend/internal/storage/sqlite/store/workflow_collaboration_store_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_collaboration_store_test.go) | modified | 578 |
| [backend/internal/storage/sqlite/store/workflow_lifecycle_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_lifecycle_store.go) | modified | 732 |
| [backend/internal/storage/sqlite/store/workflow_materialization_limits_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_materialization_limits_test.go) | new | 57 |
| [backend/internal/storage/sqlite/store/workflow_materialization_store.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_materialization_store.go) | new | 206 |
| [backend/internal/storage/sqlite/store/workflow_materialization_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_materialization_test.go) | new | 192 |
| [backend/internal/storage/sqlite/store/workflow_store_test.go](E:/code/agent-orchestrator/backend/internal/storage/sqlite/store/workflow_store_test.go) | modified | 1700 |
| [docs/customization/m3-acceptance.md](E:/code/agent-orchestrator/docs/customization/m3-acceptance.md) | new | 158 |
| [docs/customization/m3-adrs/04-lease-operator.md](E:/code/agent-orchestrator/docs/customization/m3-adrs/04-lease-operator.md) | modified | 43 |
| [docs/customization/m3-budget-review.md](E:/code/agent-orchestrator/docs/customization/m3-budget-review.md) | new | 90 |
| [docs/customization/m3-daemon-review.md](E:/code/agent-orchestrator/docs/customization/m3-daemon-review.md) | new | 86 |
| [docs/customization/m3-files.md](E:/code/agent-orchestrator/docs/customization/m3-files.md) | new | 262 |
| [docs/customization/m3-g1-review.md](E:/code/agent-orchestrator/docs/customization/m3-g1-review.md) | new | 111 |
| [docs/customization/m3-plan.md](E:/code/agent-orchestrator/docs/customization/m3-plan.md) | modified | 262 |
| [docs/customization/m3-report.md](E:/code/agent-orchestrator/docs/customization/m3-report.md) | new | 175 |
| [frontend/src/api/schema.ts](E:/code/agent-orchestrator/frontend/src/api/schema.ts) | modified | 29062 |
| [issues/2026-11-05_14-16-00-m3.csv](E:/code/agent-orchestrator/issues/2026-11-05_14-16-00-m3.csv) | modified | 12 |

## 测试规模

新增 Go 测试 6141 行，新增非生成 Go 生产代码 5252 行；比例 1.169。
所有入场后变化 Go 文件当前总量：测试 36854 行，非生成生产 44858 行；比例 0.822。此总量包含既有代码，不能当作新增行数。

## 超限申报

既有大测试文件保留原回归结构，随 M3 准入、权限及恢复边界迁移夹具；为拆文件扩大重构不在本任务范围。下表逐项列明，不隐去新增或已有超限。

| 文件 | 类型 | 当前行数 | 上限 | 超出 | 对应验收 |
|---|---|---:|---:|---:|---|
| backend/internal/cli/mcp_workflow_test.go | modified | 443 | 300 | 143 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/daemon/execution_recovery_test.go | modified | 818 | 300 | 518 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/daemon/m2_wiring_test.go | modified | 895 | 300 | 595 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/daemon/wiring_test.go | modified | 1215 | 300 | 915 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/httpd/controllers/operator_approvals_fixture_test.go | modified | 411 | 400 | 11 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/httpd/controllers/workflows_m2_authz_test.go | modified | 862 | 300 | 562 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/httpd/controllers/workflows_m2_bind_test.go | modified | 1001 | 300 | 701 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/httpd/controllers/workflows_m2_identity_test.go | modified | 880 | 300 | 580 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/httpd/controllers/workflows_test.go | modified | 877 | 300 | 577 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/integration/m2_e2e_test.go | modified | 1725 | 300 | 1425 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/integration/m2_full_flow_e2e_test.go | modified | 1790 | 300 | 1490 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/integration/m2_mcp_e2e_test.go | modified | 464 | 300 | 164 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/integration/m2_session_manager_e2e_test.go | modified | 1261 | 300 | 961 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/service/execution/coordinator_test.go | modified | 2246 | 300 | 1946 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/service/execution/reanchor_test.go | modified | 729 | 300 | 429 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/service/principal/principal_test.go | modified | 734 | 300 | 434 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/service/workflow/service_test.go | modified | 659 | 300 | 359 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/session_manager/interface_transition_history_integration_test.go | modified | 352 | 300 | 52 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/session_manager/interface_transition_test.go | modified | 3390 | 300 | 3090 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/session_manager/multi_repo_workspace_test.go | modified | 439 | 300 | 139 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/storage/sqlite/migrate_burned_versions_test.go | modified | 502 | 300 | 202 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/storage/sqlite/migrate_workflow_collaboration_test.go | modified | 399 | 300 | 99 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/storage/sqlite/store/execution_coordinator_test.go | modified | 392 | 300 | 92 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/storage/sqlite/store/m2_anomaly_test.go | modified | 715 | 300 | 415 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/storage/sqlite/store/m2_e2e_test.go | modified | 469 | 300 | 169 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/storage/sqlite/store/workflow_binding_store_test.go | modified | 1273 | 300 | 973 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/storage/sqlite/store/workflow_cdc_test.go | modified | 427 | 300 | 127 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/storage/sqlite/store/workflow_collaboration_store_test.go | modified | 578 | 300 | 278 | M1/M2 回归、M3 准入/权限/恢复 |
| backend/internal/storage/sqlite/store/workflow_store_test.go | modified | 1700 | 300 | 1400 | M1/M2 回归、M3 准入/权限/恢复 |

第六轮继承的 pricing/fetch_cache_test.go 为379行，超过300行79行；M3未再次修改，单列申报。

完整文件摘要见证据根 current-delta.json；生产和测试快照见 final-source-snapshot.json。文档在最终验收后更新状态，因此快照只固定 backend 与前端生成类型。
