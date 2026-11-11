/**
 * 启动流程的纯投影：只根据持久事实给出「当前步骤」和 Planner 状态，
 * 不触发任何操作。UNKNOWN 永远不等于成功。
 */

/** Planner attempt 的展示状态；缺省 attempt 表示尚未被调度。 */
export type PlannerStatus = "idle" | "planning" | "succeeded" | "failed" | "unknown";

export function plannerStatus(attempt: { state: string } | undefined): PlannerStatus {
	if (!attempt) return "idle";
	switch (attempt.state) {
		case "claimed":
		case "running":
		case "settling":
			return "planning";
		case "succeeded":
			return "succeeded";
		case "failed":
			return "failed";
		default:
			return "unknown";
	}
}

/** 只有 succeeded 允许提取；planning/idle 等待，failed/unknown 不改用旧结果。 */
export function canExtractPlan(status: PlannerStatus): boolean {
	return status === "succeeded";
}

/** 轮询只在可能自答的状态下进行：in-flight 或尚未被 Claim。 */
export function plannerPollInterval(status: PlannerStatus): number | false {
	return status === "planning" || status === "idle" ? 3000 : false;
}

export const startupSteps = ["create", "activate", "wait", "extract", "confirm", "resume"] as const;
export type StartupStep = (typeof startupSteps)[number] | "done";

export interface StartupFacts {
	/** Workflow 状态；没有 Workflow 时为空串。 */
	state: string;
	/** 最新一次 Planner attempt；未调度时为 undefined。 */
	planner: { state: string } | undefined;
	candidates: readonly { confirmedRevision: number }[];
	/** 独立调度快照的暂停位；未读取到时为 undefined。 */
	paused: boolean | undefined;
}

/** 返回唯一应显示的步骤；done 表示启动流程已走完。 */
export function startupStep(facts: StartupFacts): StartupStep {
	if (!facts.state) return "create";
	if (facts.state === "draft") return "activate";
	if (facts.state === "completed" || facts.state === "failed" || facts.state === "cancelled") return "done";
	if (facts.candidates.some(candidate => candidate.confirmedRevision > 0)) return facts.paused === false ? "done" : "resume";
	if (plannerStatus(facts.planner) !== "succeeded") return "wait";
	if (facts.candidates.length === 0) return "extract";
	return "confirm";
}

/** 与后端交付校验一致的完整 SHA 形状检查；不猜测、不补全。 */
export function isFullCommitSha(value: string): boolean {
	return /^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$/.test(value.trim());
}

export type CreateBlocker = "title" | "goal" | "repository" | "baseSha" | "targetRef" | "effortModel";

/** 创建表单的字段级阻塞原因，顺序固定便于稳定呈现。 */
export function createBlockers(input: {
	title: string;
	goal: string;
	repositories: readonly { baseSha?: string; targetRef?: string }[];
	model: string;
	effort: string;
}): CreateBlocker[] {
	const blockers: CreateBlocker[] = [];
	if (!input.title.trim()) blockers.push("title");
	if (!input.goal.trim()) blockers.push("goal");
	if (input.repositories.length === 0) blockers.push("repository");
	if (input.repositories.some(repo => !isFullCommitSha(repo.baseSha ?? ""))) blockers.push("baseSha");
	if (input.repositories.some(repo => !/^refs\/heads\/\S+$/.test((repo.targetRef ?? "").trim()))) blockers.push("targetRef");
	if (input.effort.trim() && !input.model.trim()) blockers.push("effortModel");
	return blockers;
}
