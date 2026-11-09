import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { useWorkflowResource, useWorkflowScope, useWorkflowTask, workflowError, type WorkflowDefinition } from "../../hooks/useWorkflows";
import { apiClient } from "../../lib/api-client";
import { useWorkflowDraft } from "../../stores/workflow-ui-store";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { WorkflowAction } from "./WorkflowAction";
import { WorkflowPlanDraft } from "./WorkflowPlanDraft";
import { WorkflowProviderSelect } from "./WorkflowProviderSelect";

/** 自然目标只建立只读 Planner；仓库 pin 与目标分支必须由用户明确选择。 */
export function WorkflowCreate({ onCreated }: { onCreated: (id: string) => void }) {
	const { t } = useTranslation();
	const scope = useWorkflowScope();
	const draftKey = `${scope[1]}:create-plan`;
	const [title, setTitle] = useWorkflowDraft(`${draftKey}:title`, "");
	const [goal, setGoal] = useWorkflowDraft(`${draftKey}:goal`, "");
	const [model, setModel] = useWorkflowDraft(`${draftKey}:model`, "");
	const [effort, setEffort] = useWorkflowDraft(`${draftKey}:effort`, "");
	const [profile, setProfile] = useWorkflowDraft(`${draftKey}:profile`, "");
	const [repos, setRepos] = useWorkflowDraft<components["schemas"]["WorkflowRepositoryInput"][]>(`${draftKey}:repos`, []);
	const projects = useQuery({ queryKey: [...scope, "projects"], queryFn: async ({ signal }) => {
		const { data, error } = await apiClient.GET("/api/v1/projects", { signal });
		if (error) throw workflowError(error); return data!.projects;
	} });
	const payload: Omit<components["schemas"]["StartWorkflowPlanRequest"], "idempotencyKey"> = { title, goal, repositories: repos, agentHarness: "codex", model, effort, profile };
	return <div className="max-w-3xl space-y-4">
		<h2 className="text-lg font-medium">{t("workflow.new")}</h2>
		<label className="block space-y-1 text-sm">{t("workflow.name", "标题")}<Input value={title} onChange={e => setTitle(e.target.value)} /></label>
		<label className="block space-y-1 text-sm">{t("workflow.goal", "目标")}<textarea className="min-h-28 w-full rounded-md border border-border bg-background p-3" value={goal} onChange={e => setGoal(e.target.value)} /></label>
		<WorkflowProviderSelect value={profile} harness="codex" onChange={selection => { setProfile(selection.profile); setModel(selection.model); setEffort(selection.effort); }} />
		<div className="grid grid-cols-2 gap-3"><label className="text-sm">{t("workflow.model", "模型")}<Input disabled={!!profile} value={model} onChange={e => setModel(e.target.value)} /></label><label className="text-sm">{t("workflow.effort", "effort")}<Input disabled={!!profile} value={effort} onChange={e => setEffort(e.target.value)} /></label></div>
		<p className="text-xs text-muted-foreground">{t("workflow.plannerSupport")}</p>
		{projects.error && <p role="alert">{projects.error.message}</p>}
		<fieldset className="space-y-3"><legend className="mb-2 text-sm">{t("workflow.repositories", "选定仓库与基线")}</legend>{projects.data?.map(project => {
			const repo = repos.find(r => r.projectId === project.id);
			return <div key={project.id} className="rounded-md border border-border p-3"><label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={!!repo} onChange={e => setRepos(e.target.checked ? [...repos, { projectId: project.id, repoKey: project.id, baseSha: "", targetRef: "refs/heads/main" }] : repos.filter(r => r.projectId !== project.id))} />{project.name || project.id}</label>
				{repo && <div className="mt-2 grid grid-cols-2 gap-2"><label className="text-xs">{t("workflow.baseSha", "基线 SHA")}<Input value={repo.baseSha} onChange={e => setRepos(repos.map(r => r === repo ? { ...r, baseSha: e.target.value } : r))} /></label><label className="text-xs">{t("workflow.targetRef", "目标分支")}<Input value={repo.targetRef} onChange={e => setRepos(repos.map(r => r === repo ? { ...r, targetRef: e.target.value } : r))} /></label></div>}
			</div>;
		})}</fieldset>
		<WorkflowAction verb="start-plan" path="/api/v1/workflows/plan" payload={payload} disabled={!title.trim() || !goal.trim() || !model.trim() || !repos.length || repos.some(r => !r.baseSha || !r.targetRef)} onSuccess={body => onCreated((body as components["schemas"]["WorkflowEnvelope"]).workflow.id)}>{t("workflow.generatePlan", "创建规划任务")}</WorkflowAction>
	</div>;
}

/** 候选编辑只修改任务字段；保存重新执行服务端图/仓库校验，确认绑定原摘要。 */
export function WorkflowPlanEditor({ definition }: { definition: WorkflowDefinition }) {
	const { t } = useTranslation();
	const id = definition.workflow.id;
	const scope = useWorkflowScope();
	const path = `/api/v1/workflows/${id}`;
	const plans = useWorkflowResource<components["schemas"]["WorkflowPlansResponse"]>(id, "plans", `${path}/plan-candidates`);
	const planner = definition.tasks.filter(task => task.planner).sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
	const task = useWorkflowTask(id, planner?.id || "");
	const attempt = task.data?.attempts[0];
	const [editing, setEditing] = useWorkflowDraft<{ source: string; turn: string; revision: number; plan: components["schemas"]["WorkflowPlanDefinition"] } | null>(`${scope[1]}:${id}:plan-draft`, null);
	return <div className="space-y-4">
		<p className="text-sm text-muted-foreground">{t("workflow.planSteps", "激活只读规划任务，等待输出完成后提取候选。确认计划后，需单独恢复调度。")}</p>
		{planner && <p className="text-sm">{planner.title} · {planner.state} · {planner.model} · {planner.profile?.split("@")[0] || t("workflow.nativeProvider")}</p>}
		{attempt && <WorkflowAction verb="prepare-plan" path={`${path}/plan-candidates`} payload={{ sourceAttemptId: attempt.id, sourceTurnId: "", expectedRevision: definition.workflow.revision }} disabled={attempt.state === "unknown"}>{t("workflow.extractPlan", "提取已完成的规划输出")}</WorkflowAction>}
		{[plans.error, task.error].filter(Boolean).map((e, i) => <p role="alert" key={i}>{e!.message}</p>)}
		{plans.data?.candidates?.map(candidate => <article key={candidate.id} className="space-y-3 rounded-lg border border-border p-4">
			<div className="flex flex-wrap justify-between gap-2 text-sm"><span>{candidate.id} · r{candidate.expectedRevision}</span><span>{candidate.confirmedRevision ? t("workflow.planConfirmed", "已确认") : t("workflow.planCandidate", "候选")}</span></div>
			<p className="break-all text-xs text-muted-foreground">{candidate.sourceAttemptId} / {candidate.sourceTurnId} · {candidate.digest}</p>
			<ul className="space-y-2">{(candidate.patch.tasks || []).map(row => <li className="text-sm" key={row.id}><strong>{row.title}</strong> · {row.agentHarness} · {row.model} · {row.profile?.split("@")[0] || t("workflow.nativeProvider")}<p className="whitespace-pre-wrap text-muted-foreground">{row.brief}</p><p className="text-xs">{(candidate.patch.taskRepositories || []).filter(r => r.taskId === row.id).map(r => `${r.repoKey}@${r.baseSha} [${r.allowedPaths?.join(", ") || "/"}]`).join("; ")}</p><p className="text-xs">{t("workflow.dependencies", "依赖")}: {(candidate.patch.dependencies || []).filter(e => e.toTaskId === row.id).map(e => e.fromTaskId).join(", ") || "—"}</p></li>)}</ul>
			{candidate.patch.retireTaskIds?.length > 0 && <p className="text-sm">{t("workflow.retire", "停用旧任务")}: {candidate.patch.retireTaskIds.join(", ")}</p>}
			<div className="flex gap-2"><Button variant="ghost" size="sm" disabled={!!candidate.confirmedRevision} onClick={() => setEditing({ source: candidate.sourceAttemptId, turn: candidate.sourceTurnId, revision: candidate.expectedRevision, plan: {
				tasks: (candidate.patch.tasks || []).map(({ id, kind, title, brief, agentHarness, model, effort, profile, maxAttempts, priority }) => ({ id, kind, title, brief, agentHarness, model, effort, profile, maxAttempts, priority })),
				dependencies: (candidate.patch.dependencies || []).map(({ fromTaskId, toTaskId }) => ({ fromTaskId, toTaskId })),
				taskRepositories: (candidate.patch.taskRepositories || []).map(({ taskId, repoKey, baseSha, allowedPaths }) => ({ taskId, repoKey, baseSha, allowedPaths })), retireTaskIds: candidate.patch.retireTaskIds || [],
			} })}>{t("workflow.editPlan", "调整候选")}</Button><WorkflowAction verb="confirm-plan" path={`${path}/plan-candidates/${candidate.id}/confirm`} payload={{ digest: candidate.digest, expectedRevision: candidate.expectedRevision }} withKey={false} disabled={!!candidate.confirmedRevision || candidate.expectedRevision !== definition.workflow.revision}>{t("workflow.confirmPlan", "确认任务图")}</WorkflowAction></div>
		</article>)}
		{editing && <section className="space-y-3 rounded-lg border border-border p-4"><h3>{t("workflow.editPlan", "调整候选")}</h3><WorkflowPlanDraft definition={definition} value={editing.plan} onChange={plan => setEditing({ ...editing, plan })} /><WorkflowAction verb="prepare-plan" path={`${path}/plan-candidates`} payload={{ sourceAttemptId: editing.source, sourceTurnId: editing.turn, expectedRevision: editing.revision, edited: editing.plan }} disabled={editing.revision !== definition.workflow.revision} onSuccess={() => setEditing(null)}>{t("workflow.validatePlan", "校验并保存候选")}</WorkflowAction></section>}
	</div>;
}
