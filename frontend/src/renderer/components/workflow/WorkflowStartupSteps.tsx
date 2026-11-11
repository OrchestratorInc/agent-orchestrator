import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { useWorkflowResource, useWorkflowTask, type WorkflowDefinition } from "../../hooks/useWorkflows";
import type { MessageKey } from "../../i18n/messages";
import { plannerPollInterval, plannerStatus, startupStep, type PlannerStatus, type StartupStep } from "../../lib/workflow-startup";

/**
 * Planner 轮询只读取持久 attempt；等待期间按 attempt 状态轮询，终态停止，
 * 因此 UNKNOWN 不会因为轮询而被当成成功。
 */
export function usePlannerAttempt(definition: WorkflowDefinition | undefined) {
	const planner = definition?.tasks.filter(task => task.planner).sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
	const id = definition?.workflow.id ?? "";
	const query = useWorkflowTask(id, planner?.id || "", data => plannerPollInterval(plannerStatus(data?.attempts?.[0])));
	const attempt = query.data?.attempts?.[0];
	return { planner, query, attempt, status: plannerStatus(attempt) };
}

/** 候选与调度暂停位共同决定启动流程停在「确认」还是「恢复调度」。 */
export function useStartupStep(definition: WorkflowDefinition | undefined, paused: boolean | undefined): StartupStep {
	const id = definition?.workflow.id ?? "";
	const plans = useWorkflowResource<components["schemas"]["WorkflowPlansResponse"]>(id, "plans", `/api/v1/workflows/${id}/plan-candidates`);
	const { attempt } = usePlannerAttempt(definition);
	return startupStep({ state: definition?.workflow.state ?? "", planner: attempt, candidates: plans.data?.candidates ?? [], paused });
}

/** 阶段名是稳定领域概念；顺序即唯一显示顺序。 */
export const stepLabels: Record<Exclude<StartupStep, "done">, MessageKey> = {
	create: "workflow.stepCreate",
	activate: "workflow.stepActivate",
	wait: "workflow.stepWait",
	extract: "workflow.stepExtract",
	confirm: "workflow.stepConfirm",
	resume: "workflow.stepResume",
};

export const plannerLabels: Record<PlannerStatus, MessageKey> = {
	idle: "workflow.plannerIdle",
	planning: "workflow.plannerPlanning",
	succeeded: "workflow.plannerSucceeded",
	failed: "workflow.plannerFailed",
	unknown: "workflow.plannerUnknown",
};

/** 提取被禁用的原因逐状态给出；succeeded 不会走到这里。 */
export const extractBlockedLabels: Record<PlannerStatus, MessageKey> = {
	idle: "workflow.extractWaiting",
	planning: "workflow.extractWaiting",
	succeeded: "workflow.extractWaiting",
	failed: "workflow.extractFailed",
	unknown: "workflow.extractUnknown",
};

/** 只显示当前步骤；done 表示启动流程已走完，不再占用界面。 */
export function WorkflowStartupSteps({ step }: { step: StartupStep }) {
	const { t } = useTranslation();
	const order = Object.keys(stepLabels) as Exclude<StartupStep, "done">[];
	const index = order.indexOf(step as Exclude<StartupStep, "done">);
	if (index < 0) return null;
	return <p role="status" className="rounded-md border border-border px-3 py-2 text-sm">
		<span className="text-xs text-muted-foreground">{index + 1} / {order.length}</span>{" · "}<strong data-step={step}>{t(stepLabels[order[index]])}</strong>
	</p>;
}
