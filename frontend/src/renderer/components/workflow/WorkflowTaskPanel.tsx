import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { useWorkflowTask, type WorkflowDefinition } from "../../hooks/useWorkflows";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { useWorkflowUi } from "../../stores/workflow-ui-store";
import { Button } from "../ui/button";
import { WorkflowAction } from "./WorkflowAction";
import { WorkflowIntervention } from "./WorkflowIntervention";

type Schemas = components["schemas"];

/** 历史 Attempt 与当前操作明确分离；查看 Session 只导航，不调用启动/绑定。 */
export function WorkflowTaskPanel({ definition, taskId, coordination, collaboration }: {
	definition: WorkflowDefinition; taskId: string; coordination?: Schemas["WorkflowCoordinationResponse"];
	collaboration?: { messages: Schemas["ListWorkflowMessagesResponse"]; requests: Schemas["ListWorkflowTaskRequestsResponse"] };
}) {
	const { t } = useTranslation();
	const id = definition.workflow.id;
	const query = useWorkflowTask(id, taskId);
	const [selected, setSelected] = useState("");
	const navigate = useNavigateToSession();
	const task = definition.tasks.find(task => task.id === taskId);
	const attempts = query.data?.attempts || [];
	const attempt = attempts.find(a => a.id === selected) || attempts[0];
	const facts = coordination?.coordination.tasks.find(task => task.taskId === taskId);
	const path = `/api/v1/workflows/${id}`;
	if (!task) return null;
	return <section className="space-y-4 rounded-lg border border-border p-4" aria-label={task.title}>
		<div className="flex items-start justify-between gap-3"><div><h3 className="font-medium">{task.title}</h3><p className="text-sm text-muted-foreground">{task.state} · {task.agentHarness} · {task.model} · {task.effort}</p></div>{attempt?.sessionId && <Button variant="outline" size="sm" onClick={() => { useWorkflowUi.getState().setOpen(false); navigate(definition.repositories.find(r => definition.taskRepositories.some(s => s.taskId === taskId && s.repoKey === r.repoKey))?.projectId, attempt.sessionId!); }}>{t("workflow.openSession", "打开现有会话")}</Button>}</div>
		<p className="whitespace-pre-wrap text-sm">{task.brief}</p>
		<p className="text-xs" title={task.profile}>{t("workflow.provider")}: {task.profile?.split("@")[0] || t("workflow.nativeProvider")}</p>
		{query.error && <p role="alert">{query.error.message}</p>}
		{!attempt?.sessionId && <p className="text-sm">{t("workflow.noSession", "尚未创建会话")}</p>}
		{facts && <dl className="grid grid-cols-2 gap-2 text-xs"><dt>{t("workflow.blockedReason", "调度原因")}</dt><dd>{facts.blockedReason || "—"}</dd><dt>{t("workflow.readiness", "会话就绪状态")}</dt><dd>{facts.sessionReadiness || "UNKNOWN"} / {facts.bindingState || "UNKNOWN"}</dd><dt>{t("workflow.lease", "租约")}</dt><dd>{facts.leaseValid ? t("workflow.valid", "有效") : t("workflow.unverified", "未验证")}</dd></dl>}
		<ul className="space-y-1 text-xs">{coordination?.coordination.reservations.filter(r => r.taskId === taskId).map(r => <li key={r.id}>{r.repoKey}: {r.scopePath || "/"} · {r.releasedAt ? t("workflow.released", "已释放") : t("workflow.reserved", "占用中")} · {r.attemptId}</li>)}</ul>
		<div className="flex flex-wrap gap-2" aria-label={t("workflow.attemptHistory", "执行历史")}>{attempts.map(a => <Button size="sm" variant={a.id === attempt?.id ? "secondary" : "ghost"} key={a.id} onClick={() => setSelected(a.id)}>#{a.attemptNo} · {a.state}</Button>)}</div>
		{attempt && <WorkflowIntervention key={attempt.id} definition={definition} attempt={attempt} current={attempt.id === attempts[0]?.id} />}
		<section className="space-y-2"><h4 className="text-sm font-medium">{t("workflow.collaboration", "协作消息")}</h4>{collaboration?.messages.messages.filter(m => m.fromTaskId === taskId || m.toTaskId === taskId).map(m => <article key={m.id} className="rounded bg-muted/30 p-3 text-sm"><p className="text-xs text-muted-foreground">{m.senderKind}: {m.fromTaskId} → {m.toTaskId || "—"} · {m.deliveryState}</p><p className="whitespace-pre-wrap">{m.body}</p></article>)}</section>
		<section className="space-y-3"><h4 className="text-sm font-medium">{t("workflow.taskRequests", "任务申请")}</h4>{collaboration?.requests.requests.filter(r => r.requesterTaskId === taskId).map(r => <article key={r.id} className="space-y-2 rounded border border-border p-3"><p className="text-sm">{r.requestKind} · {r.state} · r{r.revision}</p><pre className="overflow-auto whitespace-pre-wrap text-xs">{JSON.stringify(r.summary, null, 2)}</pre><div className="flex gap-2">{r.state === "pending" && <>{[true, false].map(approve => <WorkflowAction key={String(approve)} verb="decide" path={`${path}/task-requests/${r.id}/decide`} withKey={false} payload={{ approve, expectedRevision: r.revision, reason: approve ? "用户批准" : "用户拒绝" }}>{t(approve ? "workflow.approve" : "workflow.reject", approve ? "批准" : "拒绝")}</WorkflowAction>)}</>}{r.state === "approved" && <WorkflowAction verb="materialize" path={`${path}/task-requests/${r.id}/materialize`} payload={{ expectedRequestRevision: r.revision, expectedWorkflowRevision: definition.workflow.revision, requestSha: r.requestSha, reason: "用户确认物化任务申请" }}>{t("workflow.materialize", "应用已批准申请")}</WorkflowAction>}</div></article>)}</section>
	</section>;
}
