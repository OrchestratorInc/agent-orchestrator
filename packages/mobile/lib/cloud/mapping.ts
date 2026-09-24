import type { Project, Session } from "@aoagents/cloud-client";
import type { DashboardSession, KanbanColumn, ProjectInfo } from "../api";

/**
 * Where a cloud session sits on mobile's board.
 *
 * The daemon derives kanbanColumn server-side and sends it; the cloud contract
 * has no such field, so mobile derives it from the status enum
 * (contracts/cloud/openapi.yaml, SessionStatus) to keep both environments
 * grouping the same way.
 */
export function kanbanColumnForStatus(status: string): KanbanColumn {
	switch (status) {
		case "ci_failed":
		case "changes_requested":
			return "validating";
		case "pr_open":
		case "draft":
		case "review_pending":
			return "needs_review";
		case "approved":
		case "mergeable":
			return "ready";
		case "merged":
		case "exited":
		case "terminated":
			return "archive";
		default:
			return "building";
	}
}

export function toProjectInfo(project: Project): ProjectInfo {
	return { id: project.id, name: project.displayName, kind: "single_repo" };
}

export function toDashboardSession(session: Session): DashboardSession {
	// The wire carries sandboxProvider/desiredState/observedState flat on the
	// session; cloud-lifecycle.ts (ported from desktop in Task 14) reads them
	// nested under `cloud` alongside a sibling `runtimeConnected`. Only nest
	// when at least one lifecycle field is present, so a session with none of
	// them maps to `cloud: undefined` rather than an empty object.
	const hasLifecycle =
		session.sandboxProvider !== undefined ||
		session.desiredState !== undefined ||
		session.observedState !== undefined;

	return {
		id: session.id,
		projectId: session.projectId,
		kind: session.kind,
		status: session.status,
		kanbanColumn: kanbanColumnForStatus(session.status),
		displayStatus: null,
		// Derived from facts mobile does not have for cloud yet; the board's own
		// fallback is better than a wrong colour.
		attentionLevel: null,
		activity: session.activityState,
		harness: session.harness,
		// Cloud's `mode` is a trust level (read-only/standard/trusted), not
		// mobile's controller (chat/tui). Cloud sessions are always Chat.
		mode: "chat",
		branch: session.branch || null,
		issueId: null,
		issueTitle: null,
		userPrompt: null,
		displayName: session.displayName,
		summary: null,
		createdAt: session.createdAt,
		lastActivityAt: session.updatedAt,
		isTerminated: session.isTerminated,
		runtimeConnected: session.runtimeConnected,
		cloud: hasLifecycle
			? {
					sandboxProvider: session.sandboxProvider,
					desiredState: session.desiredState,
					observedState: session.observedState,
				}
			: undefined,
	};
}
