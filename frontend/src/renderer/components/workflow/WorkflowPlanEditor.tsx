import { useQueries, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { agentModelsQueryOptions } from "../../hooks/useAgentModelsQuery";
import { useWorkflowResource, useWorkflowScope, workflowError, type WorkflowDefinition } from "../../hooks/useWorkflows";
import { apiClient } from "../../lib/api-client";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { canExtractPlan, createBlockers, isFullCommitSha, type CreateBlocker } from "../../lib/workflow-startup";
import { useWorkflowDraft, useWorkflowUi } from "../../stores/workflow-ui-store";
import { Input } from "../ui/input";
import { Button } from "../ui/button";
import { WorkflowAction } from "./WorkflowAction";
import { WorkflowPlanDraft } from "./WorkflowPlanDraft";
import { WorkflowProviderSelect } from "./WorkflowProviderSelect";
import { extractBlockedLabels, plannerLabels, usePlannerAttempt } from "./WorkflowStartupSteps";
import type { MessageKey } from "../../i18n/messages";

/** 阻塞原因复用既有字段标签，避免为每个字段新增一份重复文案。 */
const blockerLabels: Record<CreateBlocker, MessageKey> = {
	title: "workflow.name",
	goal: "workflow.goal",
	repository: "workflow.repositories",
	baseSha: "workflow.baseSha",
	targetRef: "workflow.targetRef",
	effortModel: "workflow.model",
};

/** 自然目标只建立只读 Planner；基线 SHA 必须手填，界面不生成也不改写提交。 */
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
	// 默认分支是项目配置事实，逐个项目读取详情；详情缺失时目标分支保持为空并禁用。
	const details = useQueries({ queries: (projects.data || []).map(project => ({
		queryKey: [...scope, "project", project.id], staleTime: 30_000,
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", { signal, params: { path: { id: project.id } } });
			if (error) throw workflowError(error);
			return data?.status === "ok" && "defaultBranch" in data.project ? data.project.defaultBranch : undefined;
		},
	})) });
	// 目标分支只来自项目详情的 defaultBranch；读不到就留空并禁用，绝不猜测分支。
	const targetRefs = new Map((projects.data || []).map(project => {
		const branch = details[(projects.data || []).indexOf(project)]?.data?.trim() || "";
		return [project.id, branch ? `refs/heads/${branch}` : ""] as const;
	}));
	// 复用既有模型目录与可运行范围；选中模型后 effort 只允许该模型声明过的值。
	const catalog = useQuery({ ...agentModelsQueryOptions("codex", ""), enabled: !profile });
	const models = (catalog.data?.models || []).filter(entry => !entry.unavailableReason);
	const selectedModel = models.find(entry => entry.id === model);
	const efforts = [...new Set([selectedModel?.defaultEffort, ...(selectedModel?.efforts || [])].filter((value): value is string => !!value))];
	// 提交时以项目详情为准回填目标分支：草稿早于详情返回也不会留下错误分支。
	const submitted = repos.map(repo => ({ ...repo, targetRef: targetRefs.get(repo.projectId) || "" }));
	const blockers = createBlockers({ title, goal, repositories: submitted, model, effort });
	const payload: Omit<components["schemas"]["StartWorkflowPlanRequest"], "idempotencyKey"> = { title, goal, repositories: submitted, agentHarness: "codex", model, effort, profile };
	const toggle = (projectId: string, select: boolean) => setRepos(select
		? [...repos, { projectId, repoKey: projectId, baseSha: "", targetRef: targetRefs.get(projectId) || "" }]
		: repos.filter(repo => repo.projectId !== projectId));
	return <div className="max-w-3xl space-y-4">
		<h2 className="text-lg font-medium">{t("workflow.new")}</h2>
		<label className="block space-y-1 text-sm">{t("workflow.name", "标题")}<Input value={title} onChange={e => setTitle(e.target.value)} /></label>
		<label className="block space-y-1 text-sm">{t("workflow.goal", "目标")}<textarea className="min-h-28 w-full rounded-md border border-border bg-background p-3" value={goal} onChange={e => setGoal(e.target.value)} /></label>
		<WorkflowProviderSelect value={profile} harness="codex" onChange={selection => { setProfile(selection.profile); setModel(selection.model); setEffort(selection.effort); }} />
		<div className="grid grid-cols-2 gap-3">
			<label className="text-sm">{t("workflow.model")}<Input disabled={!!profile} value={model} onChange={e => setModel(e.target.value)} /></label>
			<label className="text-sm">{t("workflow.effort")}<Input disabled={!!profile || !model.trim()} value={effort} onChange={e => setEffort(e.target.value)} /></label>
		</div>
		{!profile && <div className="flex flex-wrap gap-1">{models.map(entry => <Button key={entry.id} type="button" size="sm" variant="ghost" onClick={() => { setModel(entry.id); setEffort(entry.defaultEffort || ""); }}>{entry.label || entry.id}</Button>)}</div>}
		{!profile && efforts.length > 0 && <div className="flex flex-wrap gap-1">{efforts.map(value => <Button key={value} type="button" size="sm" variant="ghost" onClick={() => setEffort(value)}>{value}</Button>)}</div>}
		{catalog.error && <p role="alert" className="text-xs">{catalog.error.message}</p>}
		<p className="text-xs text-muted-foreground">{t("workflow.plannerSupport")}</p>
		{projects.error && <p role="alert">{projects.error.message}</p>}
		<fieldset className="space-y-3"><legend className="mb-2 text-sm">{t("workflow.repositories")}</legend>{projects.data?.map(project => {
			const repo = repos.find(entry => entry.projectId === project.id);
			const targetRef = targetRefs.get(project.id) || "";
			const repoBaseSha = repo?.baseSha ?? "";
			const shaInvalid = !!repoBaseSha.trim() && !isFullCommitSha(repoBaseSha);
			return <div key={project.id} className="rounded-md border border-border p-3"><label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={!!repo} onChange={e => toggle(project.id, e.target.checked)} />{project.name || project.id}</label>
				{repo && <div className="mt-2 space-y-2">
					<label className="block text-xs">{t("workflow.baseSha")}<Input aria-invalid={shaInvalid} value={repoBaseSha} onChange={e => setRepos(repos.map(r => r === repo ? { ...r, baseSha: e.target.value } : r))} /></label>
					<p className="text-xs text-muted-foreground">{t("workflow.baseShaHint")}</p>
					{shaInvalid && <p role="alert" className="text-xs text-destructive">{t("workflow.baseShaInvalid")}</p>}
					<p className="text-xs">{t("workflow.targetRef")}: <span className="font-mono">{targetRef || t("workflow.targetRefUnset")}</span></p>
				</div>}
			</div>;
		})}</fieldset>
		{blockers.length > 0 && <p role="status" className="text-xs text-muted-foreground">{t("workflow.blockerSummary", { fields: blockers.map(blocker => t(blockerLabels[blocker])).join(" · ") })}</p>}
		<WorkflowAction verb="start-plan" path="/api/v1/workflows/plan" payload={payload} disabled={blockers.length > 0} onSuccess={body => onCreated((body as components["schemas"]["WorkflowEnvelope"]).workflow.id)}>{t("workflow.generatePlan", "创建规划任务")}</WorkflowAction>
	</div>;
}

/** 候选编辑只修改任务字段；保存重新执行服务端图/仓库校验，确认绑定原摘要。 */
export function WorkflowPlanEditor({ definition }: { definition: WorkflowDefinition }) {
	const { t } = useTranslation();
	const id = definition.workflow.id;
	const scope = useWorkflowScope();
	const path = `/api/v1/workflows/${id}`;
	const plans = useWorkflowResource<components["schemas"]["WorkflowPlansResponse"]>(id, "plans", `${path}/plan-candidates`);
	const { planner, query: plannerQuery, attempt, status } = usePlannerAttempt(definition);
	const navigate = useNavigateToSession();
	// Planner 会话归属首个仓库的 project；只导航，不启动或绑定。
	const plannerProject = definition.repositories.find(repo => definition.taskRepositories.some(scope => scope.repoKey === repo.repoKey))?.projectId;
	const [editing, setEditing] = useWorkflowDraft<{ source: string; turn: string; revision: number; plan: components["schemas"]["WorkflowPlanDefinition"] } | null>(`${scope[1]}:${id}:plan-draft`, null);
	return <div className="space-y-4">
		<p className="text-sm text-muted-foreground">{t("workflow.planSteps")}</p>
		{planner && <p className="text-sm">{planner.title} · {planner.state} · {planner.model} · {planner.profile?.split("@")[0] || t("workflow.nativeProvider")}</p>}
		<p role="status" className="text-sm"><strong data-planner={status}>{t(plannerLabels[status])}</strong></p>
		{attempt?.sessionId && <Button variant="outline" size="sm" onClick={() => { useWorkflowUi.getState().setOpen(false); navigate(plannerProject, attempt.sessionId!); }}>{t("workflow.openPlannerSession")}</Button>}
		{attempt && <WorkflowAction verb="prepare-plan" path={`${path}/plan-candidates`} payload={{ sourceAttemptId: attempt.id, sourceTurnId: "", expectedRevision: definition.workflow.revision }} disabled={!canExtractPlan(status)}>{t("workflow.extractPlan")}</WorkflowAction>}
		{attempt && !canExtractPlan(status) && <p className="text-xs text-muted-foreground">{t(extractBlockedLabels[status])}</p>}
		{[plans.error, plannerQuery.error].filter(Boolean).map((e, i) => <p role="alert" key={i}>{e!.message}</p>)}
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
