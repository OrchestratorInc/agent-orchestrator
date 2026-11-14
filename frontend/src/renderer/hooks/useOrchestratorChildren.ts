import { useQuery } from "@tanstack/react-query";
import type { CloudCpSessionChild, CloudCpSessionPullRequest } from "../lib/cloud-cp";
import { mockOrchestratorChildren } from "../lib/mock-data";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import {
	type AgentProvider,
	type PRState,
	type PullRequestFacts,
	type SessionActivity,
	type SessionStatus,
	toAgentProvider,
	toSessionActivity,
	toSessionStatus,
	type WorkspaceSession,
} from "../types/workspace";
import { useCloudCp } from "./useCloudCp";
import { useCloudOrg } from "./useCloudOrg";

export const orchestratorChildrenQueryKey = ["cloud-session-children"] as const;

export type OrchestratorChildView = {
	id: string;
	title: string;
	provider: AgentProvider;
	status: SessionStatus;
	activity: SessionActivity | null;
	isTerminated: boolean;
	updatedAt: string;
	prs: PullRequestFacts[];
};

function toPullRequestFacts(pr: CloudCpSessionPullRequest): PullRequestFacts {
	return {
		url: pr.url,
		number: pr.number,
		state: pr.state as PRState,
		ci: pr.ci,
		review: pr.review,
		mergeability: pr.mergeability,
		failingChecks: pr.failingChecks,
		reviewComments: pr.reviewComments,
		updatedAt: pr.updatedAt,
	};
}

export function toOrchestratorChildView(child: CloudCpSessionChild): OrchestratorChildView {
	return {
		id: child.id,
		title: child.displayName || child.id,
		provider: toAgentProvider(child.harness),
		status: toSessionStatus(child.status, child.isTerminated),
		activity: toSessionActivity({ state: child.activityState }) ?? null,
		isTerminated: child.isTerminated,
		updatedAt: child.updatedAt,
		prs: child.prs.map(toPullRequestFacts),
	};
}

export function sortChildViews(children: OrchestratorChildView[]): OrchestratorChildView[] {
	return [...children].sort((a, b) => Number(a.isTerminated) - Number(b.isTerminated));
}

export function useOrchestratorChildren(session: WorkspaceSession) {
	const { client, ready, baseUrl } = useCloudCp();
	const { org } = useCloudOrg();
	const orgId = session.cloud?.orgId ?? org?.id;
	const preview = usesPreviewWorkspaceData && session.kind === "orchestrator";
	const enabled = preview || (ready && session.kind === "orchestrator" && session.cloud !== undefined && orgId !== undefined);
	return useQuery({
		queryKey: [...orchestratorChildrenQueryKey, baseUrl, orgId ?? "", session.id],
		enabled,
		retry: 1,
		refetchInterval: preview ? false : 5000,
		queryFn: async (): Promise<OrchestratorChildView[]> => {
			if (preview) {
				return sortChildViews((mockOrchestratorChildren[session.id] ?? []).map(toOrchestratorChildView));
			}
			if (orgId === undefined) return [];
			const response = await client.listSessionChildren(orgId, session.id, { limit: 100 });
			return sortChildViews(response.items.map(toOrchestratorChildView));
		},
	});
}
