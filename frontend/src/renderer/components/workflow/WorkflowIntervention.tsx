import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { useConversation } from "../../hooks/useConversation";
import { useWorkflowResource, useWorkflowScope, type WorkflowDefinition } from "../../hooks/useWorkflows";
import { useWorkflowDraft, useWorkflowUi } from "../../stores/workflow-ui-store";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { WorkflowAction } from "./WorkflowAction";

type S = components["schemas"];

/** 干预绑定当前持久身份；页面恢复只查原回执，不能再次发送或中断。 */
export function WorkflowIntervention({ definition, attempt, current }: {
	definition: WorkflowDefinition; attempt: S["WorkflowAttemptResponse"]; current: boolean;
}) {
	const { t } = useTranslation();
	const scope = useWorkflowScope();
	const navigate = useNavigateToSession();
	const id = definition.workflow.id;
	const path = `/api/v1/workflows/${id}/attempts/${attempt.id}`;
	const binding = useWorkflowResource<S["WorkflowBindingEnvelope"]>(id, "binding", `${path}/binding`, !!attempt.sessionId);
	const observed = useWorkflowResource<S["ObserveWorkflowAttemptResponse"]>(id, "observe", `${path}/observe`, !!attempt.sessionId);
	const capabilities = useWorkflowResource<S["GetWorkflowCapabilitiesResponse"]>(id, "attempt-capabilities", `${path}/capabilities`, !!attempt.sessionId, current ? 30000 : false);
	const receipts = useWorkflowResource<S["WorkflowInterventionReceiptsResponse"]>(id, "intervention-receipts", `${path}/interventions`, true, 5000);
	const conversation = useConversation(attempt.sessionId);
	const [text, setText] = useWorkflowDraft(`${scope[1]}:${path}:guidance`, "");
	const [model, setModel] = useWorkflowDraft(`${scope[1]}:${path}:replan-model`, definition.tasks.find(task => task.planner)?.model || "");
	const [effort, setEffort] = useWorkflowDraft(`${scope[1]}:${path}:replan-effort`, "");
	const [now, setNow] = useState(Date.now);
	useEffect(() => {
		const timer = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(timer);
	}, []);
	const generation = binding.data?.binding.controllerGeneration;
	const live = current && observed.data?.live === true && observed.data.observation?.sessionId === attempt.sessionId && observed.data.observation?.controllerGeneration === generation;
	const turn = conversation.snapshot?.turns.find(turn => turn.state === "running" && !turn.rolledBack)?.providerTurnId;
	const capability = capabilities.data?.capabilities.find(c => !c.expired && Date.parse(c.expiresAt) > now && c.observedSessionId === attempt.sessionId && c.generation === generation && c.readiness === "ready" && ["authorized", "not_applicable"].includes(c.authState));
	const supportsSteer = capability?.steer === "supported" && !!turn;
	const history = receipts.data?.interventions || [];
	const stop = [...history].reverse().find(r => r.mode === "stop" && r.attemptId === attempt.id && r.sessionId === attempt.sessionId && r.controllerGeneration === generation);
	return <div className="space-y-3 border-t border-border pt-3">
		<p className="break-all text-xs">Attempt {attempt.id} · Session {attempt.sessionId || "—"} · {generation || "UNKNOWN"}</p>
		{[binding.error?.message, observed.error?.message, conversation.error, capabilities.error?.message, receipts.error?.message].filter(Boolean).map((error, i) => <p key={i} role="alert" className="text-xs">{error}</p>)}
		<label className="block text-sm">{t("workflow.guidance")}<textarea className="mt-1 min-h-20 w-full rounded-md border border-border bg-background p-2" value={text} onChange={e => setText(e.target.value)} /></label>
		<div className="flex flex-wrap gap-2">{(["steer", "queue", "stop"] as const).map(mode => <WorkflowAction key={mode} verb="intervention" path={`${path}/interventions`} keyField="clientMessageId" payload={{ mode, ...(mode !== "stop" ? { text } : {}), controllerGeneration: generation, ...(mode === "steer" ? { targetProviderTurnId: turn } : {}) }} disabled={!live || !generation || (mode !== "stop" && !text.trim()) || (mode === "steer" && !supportsSteer) || (mode === "queue" && capability?.queue !== "supported") || (mode === "stop" && (capability?.interrupt !== "supported" || !!stop && stop.state !== "rejected"))}>{t(`workflow.${mode}`)}</WorkflowAction>)}</div>
		{!supportsSteer && <p className="text-xs text-muted-foreground">{t("workflow.steerUnavailable")}</p>}
		{attempt.sessionId && history.some(receipt => receipt.mode === "queue" && receipt.state === "queued") && <Button variant="outline" size="sm" onClick={() => {
			useWorkflowUi.getState().setOpen(false);
			navigate(definition.repositories.find(repo => definition.taskRepositories.some(scope => scope.taskId === attempt.taskId && scope.repoKey === repo.repoKey))?.projectId, attempt.sessionId!);
		}}>{t("workflow.manageQueue")}</Button>}
		{history.map(receipt => <p key={receipt.id} role="status" className="text-sm">{receipt.mode}: {receipt.state} · {receipt.reasonCode} · {receipt.clientMessageId}</p>)}
		{current && stop?.state === "stop_confirmed" && <div className="space-y-2"><label className="block text-sm">{t("workflow.replanModel")}<Input value={model} onChange={e => setModel(e.target.value)} /></label><label className="block text-sm">{t("workflow.effort")}<Input value={effort} onChange={e => setEffort(e.target.value)} /></label><WorkflowAction verb="start-replan" path={`/api/v1/workflows/${id}/replan`} payload={{ stopInterventionId: stop.id, expectedRevision: definition.workflow.revision, agentHarness: "codex", model, effort }} disabled={!model.trim() || definition.tasks.some(task => task.planner && !["succeeded", "cancelled", "failed"].includes(task.state))}>{t("workflow.startReplan")}</WorkflowAction></div>}
	</div>;
}
