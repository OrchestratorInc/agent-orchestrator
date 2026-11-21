/**
 * Which of the desktop session topbar's actions (SessionTopbarActions.tsx) a
 * phone session header carries. Pure, so the rule is tested under Node.
 *
 * - a worker leads with its project's Orchestrator;
 * - an orchestrator offers New task (its workers are under it on home);
 * - a standalone agent has no project, so neither.
 */
export type SessionTopbarActionId = "orchestrator" | "new-task";

export function sessionTopbarActionIds(input: { orchestrator: boolean; projectId?: string }): SessionTopbarActionId[] {
	if (!input.projectId) return [];
	return input.orchestrator ? ["new-task"] : ["orchestrator"];
}
