import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { useWorkflowResource } from "../../hooks/useWorkflows";
import { Input } from "../ui/input";
import { WorkflowAction } from "./WorkflowAction";

type S = components["schemas"];

/** 当前质量必须同时匹配工件、策略和有效期；历史 PASS 仅作为历史证据。 */
export function currentArtifactGate(artifact: S["ArtifactSnapshot"], quality: S["WorkflowQualitySummaryResponse"], now: number, currentAttemptId: string | undefined) {
	const latest = quality.artifacts.filter(a => a.taskId === artifact.taskId && a.repo.repoKey === artifact.repo.repoKey && a.attemptId === currentAttemptId)
		.every(a => a.artifactRevision <= artifact.artifactRevision);
	if (!currentAttemptId || artifact.attemptId !== currentAttemptId || !latest || quality.truncated || artifact.state !== "verified" || !quality.currentPolicy) return undefined;
	const gate = quality.gates.filter(g => g.artifactId === artifact.id).sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
	return gate && gate.artifactFingerprint === artifact.fingerprint && gate.policyDigest === quality.currentPolicy.digest && Date.parse(gate.expiresAt) > now ? gate : undefined;
}

/** 逐仓呈现质量与集成，不从单仓 PASS 推导多仓整体成功。 */
export function WorkflowQualityPanel({ id, quality, tasks }: { id: string; quality: S["WorkflowQualitySummaryResponse"]; tasks: S["CoordinationTaskView"][] }) {
	const { t } = useTranslation();
	const path = `/api/v1/workflows/${id}`;
	const [policy, setPolicy] = useState("");
	const [selected, setSelected] = useState<string[]>([]);
	const [now, setNow] = useState(Date.now);
	useEffect(() => {
		const timer = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(timer);
	}, []);
	return <section className="space-y-4">
		<h3 className="text-sm font-medium">{t("workflow.quality", "质量与逐仓集成")}</h3>
		{quality.truncated && <p role="status">{t("workflow.truncated", "结果已截断，请缩小范围后查看。")}</p>}
		<div className="flex flex-wrap items-end gap-2"><label className="text-sm">{t("workflow.policy", "已登记的质量策略 ID")}<Input value={policy} onChange={e => setPolicy(e.target.value)} /></label><WorkflowAction verb="quality-policy" path={`${path}/quality-policy`} withKey={false} payload={{ policyId: policy, expectedRevision: quality.currentPolicy?.revision || 0 }} disabled={!policy.trim()}>{t("workflow.applyPolicy", "应用策略")}</WorkflowAction></div>
		{quality.artifacts.map(artifact => {
			const gate = currentArtifactGate(artifact, quality, now, tasks.find(task => task.taskId === artifact.taskId)?.attemptId);
			const reviews = quality.reviews.filter(r => r.artifactId === artifact.id);
			return <article key={artifact.id} className="space-y-2 rounded border border-border p-3">
				<label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={selected.includes(artifact.id)} onChange={e => setSelected(e.target.checked ? [...selected, artifact.id] : selected.filter(id => id !== artifact.id))} />{artifact.repo.repoKey} · {artifact.taskId} · r{artifact.artifactRevision} · {artifact.state}</label>
				<dl className="grid grid-cols-[auto_1fr] gap-x-4 text-xs"><dt>{t("workflow.targetRef", "目标分支")}</dt><dd>{artifact.repo.targetRef}</dd><dt>HEAD</dt><dd className="break-all">{artifact.git.headSha}</dd><dt>{t("workflow.fingerprint", "工件摘要")}</dt><dd className="break-all">{artifact.fingerprint}</dd><dt>Gate</dt><dd>{gate?.state || t("workflow.noCurrentEvidence", "缺少当前有效证据")}</dd></dl>
				{quality.gates.filter(g => g.artifactId === artifact.id).map(g => <p key={g.id} className="text-xs">{g.id} · {g.state} · {g === gate ? t("workflow.currentEvidence", "当前证据") : t("workflow.historyEvidence", "历史或失效证据")} · {(g.results || []).map(r => `${r.ruleId}: ${r.state} (${r.exitCode ?? "UNKNOWN"})`).join("; ")}</p>)}
				{reviews.map(review => <div key={review.id} className="space-y-1 text-xs"><p>Review · {review.state} / {review.verdict} · {t("workflow.reviewRecorded", "历史回执；操作时重新核验目标分支与工件")}</p><p className="break-all">{t("workflow.targetSha", "审查目标 SHA")}: {review.targetSha}</p>{(review.findings || []).map((finding, i) => <p key={i}>{finding.path}:{finding.line} · P{finding.priority} · {finding.reason}</p>)}{review.state === "running" && <WorkflowReviewFinish path={`${path}/reviews/${review.id}/finish`} review={review} canApprove={gate?.state === "PASS"} />}</div>)}
				<div className="flex flex-wrap gap-2"><WorkflowAction verb="gate" path={`${path}/artifacts/${artifact.id}/gates`} payload={{ artifactRevision: artifact.artifactRevision }}>{t("workflow.runGate", "运行检查")}</WorkflowAction><WorkflowAction verb="review" path={`${path}/artifacts/${artifact.id}/reviews`} payload={{ artifactRevision: artifact.artifactRevision }}>{t("workflow.requestReview", "创建审查")}</WorkflowAction><WorkflowAction verb="commit" path={`${path}/artifacts/${artifact.id}/commit`} payload={{ artifactRevision: artifact.artifactRevision }}>{t("workflow.taskCommit", "创建任务提交")}</WorkflowAction></div>
			</article>;
		})}
		<WorkflowAction verb="preview" path={`${path}/integrations/preview`} payload={{ artifactIds: selected }} disabled={!selected.length}>{t("workflow.previewIntegration", "预演所选工件集成")}</WorkflowAction>
		{quality.integrations.map(plan => <WorkflowIntegration key={plan.id} id={id} planId={plan.id} />)}
		{quality.operations.map(operation => <WorkflowIntegrationOperation key={operation.id} id={id} operationId={operation.id} />)}
	</section>;
}

/** 人工结论携带结构化问题，服务端仍验证独立身份、目标 SHA 和当前 Gate。 */
function WorkflowReviewFinish({ path, review, canApprove }: { path: string; review: S["QualityReview"]; canApprove: boolean }) {
	const { t } = useTranslation();
	const [finding, setFinding] = useState({ path: "", line: 1, priority: 2, reason: "" });
	const valid = finding.path.trim() && finding.reason.trim() && Number.isInteger(finding.line) && finding.line > 0 && Number.isInteger(finding.priority) && finding.priority >= 0 && finding.priority <= 3;
	return <div className="space-y-2"><label className="block">{t("workflow.findingPath", "问题文件")}<Input value={finding.path} onChange={e => setFinding({ ...finding, path: e.target.value })} /></label><div className="flex gap-2"><label>{t("workflow.findingLine", "行号")}<Input type="number" min="1" value={finding.line} onChange={e => setFinding({ ...finding, line: Number(e.target.value) })} /></label><label>{t("workflow.findingPriority", "优先级")}<Input type="number" min="0" max="3" value={finding.priority} onChange={e => setFinding({ ...finding, priority: Number(e.target.value) })} /></label></div><label className="block">{t("workflow.findingReason", "问题说明")}<Input value={finding.reason} onChange={e => setFinding({ ...finding, reason: e.target.value })} /></label><div className="flex gap-2"><WorkflowAction verb="finish-review" path={path} withKey={false} payload={{ expectedRevision: review.revision, findings: [], verdict: "approved" }} disabled={!canApprove}>{t("workflow.approveReview", "批准审查")}</WorkflowAction><WorkflowAction verb="finish-review" path={path} withKey={false} payload={{ expectedRevision: review.revision, findings: [finding], verdict: "changes_requested" }} disabled={!valid}>{t("workflow.requestChanges", "请求修复")}</WorkflowAction></div></div>;
}

/** 冻结预演 entries 原样参与精确审批，保留顺序与 null，不重建或过滤。 */
function WorkflowIntegration({ id, planId }: { id: string; planId: string }) {
	const { t } = useTranslation();
	const path = `/api/v1/workflows/${id}/integrations/${planId}`;
	const query = useWorkflowResource<S["IntegrationPlanResponse"]>(id, "integration", path);
	if (query.error) return <p role="alert">{query.error.message}</p>;
	if (!query.data) return <p>{t("workflow.loading")}</p>;
	const { plan, repositories } = query.data;
	return <article className="space-y-3 rounded border border-border p-3"><p className="text-sm">{plan.id} · {plan.status} · r{plan.revision}</p><div className="overflow-auto"><table className="w-full text-left text-xs"><thead><tr><th>{t("workflow.repo", "仓库")}</th><th>{t("workflow.targetRef", "目标分支")}</th><th>SHA</th><th>{t("workflow.result", "结果")}</th></tr></thead><tbody>{repositories.map(repo => <tr key={repo.repoKey}><td>{repo.repoKey}</td><td>{repo.targetRef}</td><td className="max-w-60 break-all">{repo.expectedTargetSha} → {repo.mergeCommitSha}</td><td>{repo.status}<pre className="whitespace-pre-wrap">{JSON.stringify(repo.conflictPaths)}</pre></td></tr>)}</tbody></table></div><p className="break-all text-xs">{plan.planDigest}</p><WorkflowAction verb="apply" path={`${path}/apply`} payload={{ entries: repositories, expectedRevision: plan.revision, planDigest: plan.planDigest }} disabled={plan.status !== "clean"}>{t("workflow.applyIntegration", "批准本地逐仓应用")}</WorkflowAction><p className="text-xs text-muted-foreground">{t("workflow.pushUnavailable", "当前未装配可用的网络 Push 边界。")}</p></article>;
}

/** 操作回执保留整体及逐仓原始状态，PARTIAL/UNKNOWN 不折叠为成功。 */
function WorkflowIntegrationOperation({ id, operationId }: { id: string; operationId: string }) {
	const { t } = useTranslation();
	const path = `/api/v1/workflows/${id}/integration-operations/${operationId}`;
	const query = useWorkflowResource<S["IntegrationOperationResponse"]>(id, "integration-operation", path);
	if (query.error) return <p role="alert">{query.error.message}</p>;
	if (!query.data) return null;
	return <article className="space-y-2 rounded border border-border p-3 text-sm"><p>{query.data.operation.action} · {query.data.operation.state}</p>{query.data.repositories.map(repo => <p key={repo.repoKey}>{repo.repoKey} · {repo.state} · {repo.observedSha}</p>)}<WorkflowAction verb="reconcile-integration" path={`${path}/reconcile`} withKey={false} payload={{ expectedRevision: query.data.operation.revision }}>{t("workflow.reconcile", "核对原操作结果")}</WorkflowAction></article>;
}
