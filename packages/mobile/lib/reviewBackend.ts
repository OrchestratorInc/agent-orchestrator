import { useMemo } from "react";
import {
	cancelSessionReview,
	getAgentModels,
	getAgents,
	getProject,
	getSession,
	getSessionPR,
	getSessionReviews,
	killSessionReviewer,
	mergeSessionPR,
	requestSessionRereview,
	resolveSessionReviewComment,
	restoreSessionReviewer,
	sendMessage,
	setSessionAutoInjectCI,
	setSessionAutoInjectReview,
	setSessionAutoReview,
	switchSessionReviewer,
	triggerSessionReview,
	type AgentModelCatalog,
	type DashboardSession,
	type ReviewerAgentConfig,
	type ReviewRun,
	type SessionPRSummary,
	type SessionReviews,
} from "./api";
import { cloudAgentLabel } from "./cloud/agents";
import { formatReviewSummaryMessage } from "./reviewFeedback";
import type { ReviewPolicyKey } from "./review-actions-sheet.types";
import { defaultReviewerHarness, reviewerChoices, reviewerSwitchSelection } from "./reviewerControls";
import { useCloud } from "./cloud/CloudProvider";
import { cloudErrorMessage } from "./cloud/errors";
import { cloudSessionRow, isCloudHost } from "./cloud/mapping";
import { cloudPRSummary, cloudSessionReviews } from "./cloud/pullRequests";
import type { CloudClient } from "./cloud/client";
import type { ServerConfig } from "./config";
import { useApp } from "./store";

/**
 * Where a pull request's review page reads and acts: the paired machine's
 * daemon, or Orchestrator Cloud for a cloud session. Both answer in the same
 * shapes (cloud/pullRequests.ts maps the control plane's), so the page is one
 * page. What Cloud cannot do (stop or restore a reviewer session; reviewers
 * there run as conversations) is left out, and the page leaves out its control.
 */
export type ReviewBackend = {
	/** Changes when the machine, its credential, or the Cloud account does. */
	key: string;
	cloud: boolean;
	reviews(sessionId: string): Promise<SessionReviews>;
	pullRequests(sessionId: string): Promise<SessionPRSummary[]>;
	/** An archived worker the board no longer lists. */
	worker(sessionId: string): Promise<DashboardSession | null>;
	/** `created` is false when every head commit was already reviewed. */
	trigger(sessionId: string): Promise<{ created: boolean }>;
	cancel(sessionId: string): Promise<void>;
	/** A message from the person, as the worker's chat sends it. */
	sendToWorker(sessionId: string, text: string): Promise<void>;
	/** Sends a finished Orchestrator review to the worker (Cloud sends its stored run, and records it). */
	sendRun(sessionId: string, run: ReviewRun): Promise<void>;
	merge(sessionId: string, pr: Pick<SessionPRSummary, "number" | "url" | "headSha">): Promise<void>;
	restoreReviewer?(sessionId: string): Promise<void>;
	killReviewer?(sessionId: string): Promise<SessionReviews>;

	// ---- The review actions sheet
	settings(sessionId: string): Promise<ReviewSettings>;
	setReviewer(sessionId: string, harness: string, agentConfig: ReviewerAgentConfig): Promise<void>;
	setPolicy(sessionId: string, key: ReviewPolicyKey, value: boolean): Promise<void>;
	/** The reviewer's models; Cloud picks the reviewer's agent only. */
	reviewerModels?(harness: string, signal: AbortSignal): Promise<AgentModelCatalog>;
	/** Ask a GitHub reviewer to review again; a daemon's only. */
	rerequest?(sessionId: string, prUrl: string, reviewerId: string): Promise<void>;
	/** Resolve a review comment on GitHub; a daemon's only. */
	resolveComment?(sessionId: string, prUrl: string, commentUrl: string): Promise<void>;
};

/** The review actions sheet's reviewer choice and automation switches. */
export type ReviewSettings = {
	/** Reviewers that can run now, with any the session already names. */
	reviewers: { id: string; label: string; selectable: boolean }[];
	/** The session's own choice, "" for the default. */
	selectedReviewer: string;
	/** Who reviews when the session names nobody. */
	defaultReviewer: string;
	reviewerConfig: ReviewerAgentConfig;
	policies: Record<ReviewPolicyKey, boolean>;
};

export function daemonReviewBackend(config: ServerConfig): ReviewBackend {
	return {
		key: JSON.stringify([config.hostId, config.host, config.httpPort, config.secure, config.password]),
		cloud: false,
		reviews: (sessionId) => getSessionReviews(config, sessionId),
		pullRequests: (sessionId) => getSessionPR(config, sessionId),
		worker: (sessionId) => getSession(config, sessionId),
		trigger: async (sessionId) => ({ created: (await triggerSessionReview(config, sessionId)).created }),
		cancel: (sessionId) => cancelSessionReview(config, sessionId),
		sendToWorker: async (sessionId, text) => { await sendMessage(config, sessionId, text, { userAuthored: true }); },
		sendRun: async (sessionId, run) => { await sendMessage(config, sessionId, formatReviewSummaryMessage(run), { userAuthored: true }); },
		merge: (_sessionId, pr) => mergeSessionPR(config, pr),
		restoreReviewer: (sessionId) => restoreSessionReviewer(config, sessionId),
		killReviewer: (sessionId) => killSessionReviewer(config, sessionId),
		settings: async (sessionId) => {
			const [catalog, session] = await Promise.all([getAgents(config), getSession(config, sessionId)]);
			const project = session.projectId ? await getProject(config, session.projectId).catch(() => undefined) : undefined;
			return {
				reviewers: reviewerChoices(catalog).map((agent) => ({ id: agent.id, label: agent.label || agent.id, selectable: agent.selectable })),
				selectedReviewer: session.reviewerHarness || "",
				defaultReviewer: defaultReviewerHarness(project?.config?.reviewers, session.harness ?? undefined),
				reviewerConfig: session.reviewerConfig ?? {},
				policies: {
					autoReviewEnabled: session.autoReviewEnabled ?? false,
					autoInjectReview: session.autoInjectReview ?? true,
					autoInjectCI: session.autoInjectCI ?? true,
				},
			};
		},
		setReviewer: async (sessionId, harness, agentConfig) => {
			const selection = reviewerSwitchSelection(harness, agentConfig);
			await switchSessionReviewer(config, sessionId, selection.harness, selection.agentConfig);
		},
		setPolicy: async (sessionId, key, value) => {
			if (key === "autoReviewEnabled") await setSessionAutoReview(config, sessionId, value);
			else if (key === "autoInjectReview") await setSessionAutoInjectReview(config, sessionId, value);
			else await setSessionAutoInjectCI(config, sessionId, value);
		},
		reviewerModels: (harness, signal) => getAgentModels(config, harness, undefined, signal),
		rerequest: (sessionId, prUrl, reviewerId) => requestSessionRereview(config, sessionId, prUrl, reviewerId),
		resolveComment: (sessionId, prUrl, commentUrl) => resolveSessionReviewComment(config, sessionId, prUrl, commentUrl),
	};
}

/** The control plane's refusals in its own words, as the rest of the cloud screens show them. */
function inCloudWords<A extends unknown[], R>(run: (...args: A) => Promise<R>): (...args: A) => Promise<R> {
	return async (...args) => {
		try {
			return await run(...args);
		} catch (cause) {
			throw new Error(cloudErrorMessage(cause), { cause });
		}
	};
}

export function cloudReviewBackend(client: CloudClient, orgId: string): ReviewBackend {
	return {
		key: JSON.stringify(["cloud", client.baseUrl, orgId]),
		cloud: true,
		reviews: inCloudWords(async (sessionId) => cloudSessionReviews(await client.getSessionReviewState(orgId, sessionId))),
		pullRequests: inCloudWords(async (sessionId) => (await client.listSessionPullRequests(orgId, sessionId)).pullRequests.map(cloudPRSummary)),
		worker: async (sessionId) => {
			try {
				return cloudSessionRow((await client.getSession(orgId, sessionId)).session);
			} catch {
				return null;
			}
		},
		trigger: inCloudWords(async (sessionId) => ({ created: (await client.triggerSessionReviews(orgId, sessionId)).created })),
		cancel: inCloudWords(async (sessionId) => { await client.cancelSessionReviews(orgId, sessionId); }),
		sendToWorker: inCloudWords(async (sessionId, text) => { await client.sendMessage(orgId, sessionId, { text }); }),
		sendRun: inCloudWords(async (sessionId, run) => { await client.sendReviewToWorker(orgId, sessionId, run.id); }),
		merge: inCloudWords(async (sessionId, pr) => {
			if (!pr.headSha) throw new Error(`PR #${pr.number} has no head commit yet; refresh and try again.`);
			await client.mergePullRequest(orgId, sessionId, pr.number, pr.url, pr.headSha);
		}),
		// The reviewers the control plane can run for this session, and its
		// settings. The control plane names the reviewer it would use when the
		// session names none.
		settings: inCloudWords(async (sessionId) => {
			const [{ session }, state] = await Promise.all([client.getSession(orgId, sessionId), client.getSessionReviewState(orgId, sessionId)]);
			const selected = session.reviewerHarness || "";
			const ids = [...new Set([...(state.availableReviewerHarnesses ?? []), ...(selected ? [selected] : [])])];
			const available = new Set(state.availableReviewerHarnesses ?? []);
			return {
				reviewers: ids.map((id) => ({ id, label: cloudAgentLabel(id), selectable: available.has(id) })),
				selectedReviewer: selected,
				defaultReviewer: state.reviewerHarness || session.harness,
				reviewerConfig: {},
				policies: {
					autoReviewEnabled: session.autoReviewEnabled === true,
					autoInjectReview: session.autoInjectReview ?? true,
					autoInjectCI: session.autoInjectCI ?? true,
				},
			};
		}),
		setReviewer: inCloudWords(async (sessionId, harness) => { await client.updateSessionPreferences(orgId, sessionId, { reviewerHarness: harness }); }),
		setPolicy: inCloudWords(async (sessionId, key, value) => { await client.updateSessionPreferences(orgId, sessionId, { [key]: value }); }),
	};
}

/**
 * The review backend for the host this screen is scoped to, or null until it
 * can be reached; `signedOut` when it is Cloud and nobody is signed in, which
 * no amount of waiting fixes.
 */
export function useReviewBackend(): { backend: ReviewBackend | null; signedOut: boolean } {
	const { config, currentHostId } = useApp();
	const { client, org, status } = useCloud();
	const cloud = isCloudHost(currentHostId);
	const orgId = org?.id;
	// Apart, so a machine's backend never changes with the Cloud account's state.
	const daemon = useMemo(() => !cloud && config ? daemonReviewBackend(config) : null, [cloud, config]);
	const remote = useMemo(() => cloud && orgId ? cloudReviewBackend(client, orgId) : null, [client, cloud, orgId]);
	return { backend: cloud ? remote : daemon, signedOut: cloud && status === "signed-out" };
}
