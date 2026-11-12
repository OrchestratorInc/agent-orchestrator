import { CLOUD_API_PREFIX, cloudAddressIsSafe, cloudWebSocketBase } from "./config";
import { CloudAuthError, CloudError, isPlanRefusal } from "./errors";
import type {
	CloudArtifact,
	CloudArtifactAccess,
	CloudArtifactShareList,
	CloudArtifactVersionSummary,
	CloudAttachment,
	CloudBillingSummary,
	CloudChatEventsPage,
	CloudInterfaceTransition,
	CloudInterfaceTransitionStatus,
	CloudChatModels,
	CloudGitHubInstallation,
	CloudGitHubRepository,
	CloudMe,
	CloudNotificationPage,
	CloudOrganization,
	CloudPage,
	CloudProject,
	CloudProjectConfig,
	CloudProviderConnection,
	CloudSendMessageInput,
	CloudSession,
	CloudSessionPin,
	CloudSessionPreferences,
	CloudSessionPullRequests,
	CloudSessionReviewState,
	CloudTerminalKind,
	CloudTerminalTicket,
	CloudWorkspaceFile,
	CloudWorkspaceReview,
	CloudWorkspaceRevision,
	CloudWorkspaceScope,
	CloudWorkspaceSearch,
	CloudWorkspaceTree,
	CreateCloudSessionInput,
} from "./types";

type Query = Record<string, string | number | boolean | undefined>;

type SendOptions = {
	query?: Query;
	body?: unknown;
	idempotencyKey?: string;
	signal?: AbortSignal;
	timeoutMs?: number;
};

export type CloudClientOptions = {
	baseUrl: string;
	/** The Orchestrator account access token. Fetched fresh for every request; null means signed out. */
	getToken: () => Promise<string | null>;
	fetch?: typeof fetch;
	/**
	 * expo/fetch on the phone: its response body streams (React Native's global
	 * fetch does not expose one under Hermes) and it sends raw bytes. The
	 * session event stream and attachment uploads use it.
	 */
	nativeFetch?: typeof fetch;
	/** A fresh Idempotency-Key. Injected so tests need no native crypto. */
	newKey: () => string;
	/** A 403 usually means the selected organization is stale; the owner re-resolves it. */
	onForbidden?: () => void;
	/**
	 * The organization's plan refused the request (402 PLAN_REQUIRED, a full
	 * slot, a usage limit). Wherever it came from, the owner explains it once.
	 */
	onPlanRefused?: (error: CloudError) => void;
};

export type ListOptions = { cursor?: string; limit?: number; signal?: AbortSignal };

const DEFAULT_TIMEOUT_MS = 20_000;
/** The header an upload names its body's SHA-256 in (the control plane's artifact header). */
const ARTIFACT_SHA256_HEADER = "X-AO-Artifact-SHA256";
// The ticket route waits up to 8s for the agent terminal; the model list asks the live worker.
const WORKER_TIMEOUT_MS = 45_000;

function seg(value: string): string {
	return encodeURIComponent(value);
}

function buildUrl(baseUrl: string, path: string, query?: Query): string {
	let url = `${baseUrl}${CLOUD_API_PREFIX}${path}`;
	const params = Object.entries(query ?? {}).filter((entry): entry is [string, string | number | boolean] => entry[1] !== undefined && entry[1] !== "");
	if (params.length) url += `?${params.map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`).join("&")}`;
	return url;
}

async function errorFromResponse(response: Response): Promise<CloudError> {
	let message = `Orchestrator Cloud request failed (${response.status}).`;
	let code: string | undefined;
	let requestId: string | undefined;
	let details: Record<string, unknown> | undefined;
	let resetsAt: string | undefined;
	try {
		const body = JSON.parse(await response.text()) as Record<string, unknown> | null;
		if (typeof body?.message === "string" && body.message) message = body.message;
		else if (typeof body?.error === "string" && body.error) message = body.error;
		if (typeof body?.code === "string" && body.code) code = body.code;
		if (typeof body?.requestId === "string" && body.requestId) requestId = body.requestId;
		if (body?.details && typeof body.details === "object") details = body.details as Record<string, unknown>;
		// A plan refusal carries its limit's reset time beside the envelope.
		if (typeof body?.resetsAt === "string" && body.resetsAt) resetsAt = body.resetsAt;
	} catch {
		// Not JSON: keep the status message.
	}
	if (response.status === 401) return new CloudAuthError(message, { status: 401, code, requestId });
	return new CloudError(message, { status: response.status, code, requestId, details, resetsAt });
}

/**
 * A small typed client for the control plane's user routes.
 *
 * Mirrors the desktop renderer's client (frontend/src/renderer/lib/cloud-cp/
 * client.ts) for the routes the phone uses. Request bodies carry only
 * documented fields: the control plane decodes them strictly and refuses any
 * unknown field.
 */
export function createCloudClient(options: CloudClientOptions) {
	const baseUrl = options.baseUrl.replace(/\/+$/, "");
	const doFetch: typeof fetch = options.fetch ?? ((input, init) => fetch(input, init));

	// Checked once: the address is fixed for the client's life.
	const addressSafe = cloudAddressIsSafe(baseUrl);

	async function bearer(): Promise<string> {
		if (!addressSafe) throw new CloudError("Orchestrator Cloud's address must use https.", { status: 0, code: "insecure_address" });
		let token: string | null;
		try {
			token = await options.getToken();
		} catch (cause) {
			// A refused refresh signs the account out; anything else is the network.
			const message = cause instanceof Error ? cause.message : "";
			if (message.startsWith("Session expired")) throw new CloudAuthError(message, { status: 401, code: "session_expired" });
			throw new CloudError("Couldn't reach Orchestrator Cloud.", { status: 0, cause });
		}
		if (!token) throw new CloudAuthError("Sign in to Orchestrator Cloud first.", { status: 401, code: "no_token" });
		return token;
	}

	async function send(method: string, path: string, init: SendOptions = {}): Promise<Response> {
		const token = await bearer();
		const headers: Record<string, string> = { Authorization: `Bearer ${token}`, Accept: "application/json" };
		if (init.body !== undefined) headers["Content-Type"] = "application/json";
		if (init.idempotencyKey !== undefined) headers["Idempotency-Key"] = init.idempotencyKey;

		const controller = new AbortController();
		const abort = () => controller.abort();
		if (init.signal?.aborted) controller.abort();
		init.signal?.addEventListener("abort", abort);
		const timer = setTimeout(abort, init.timeoutMs ?? DEFAULT_TIMEOUT_MS);
		let response: Response;
		try {
			response = await doFetch(buildUrl(baseUrl, path, init.query), {
				method,
				headers,
				body: init.body === undefined ? undefined : JSON.stringify(init.body),
				signal: controller.signal,
			});
		} catch (cause) {
			if (init.signal?.aborted) throw Object.assign(new Error("Request cancelled"), { name: "AbortError" });
			throw new CloudError("Couldn't reach Orchestrator Cloud.", { status: 0, cause });
		} finally {
			clearTimeout(timer);
			init.signal?.removeEventListener("abort", abort);
		}
		if (!response.ok) {
			throw await refusal(response);
		}
		return response;
	}

	/** A refused request's error, once the owner has heard of the refusals it acts on. */
	async function refusal(response: Response): Promise<CloudError> {
		if (response.status === 403) options.onForbidden?.();
		const error = await errorFromResponse(response);
		if (isPlanRefusal(error)) options.onPlanRefused?.(error);
		return error;
	}

	async function json<T>(method: string, path: string, init?: SendOptions): Promise<T> {
		const response = await send(method, path, init);
		const text = await response.text();
		return (text ? JSON.parse(text) : {}) as T;
	}

	const org = (orgId: string) => `/orgs/${seg(orgId)}`;
	const session = (orgId: string, sessionId: string) => `${org(orgId)}/sessions/${seg(sessionId)}`;

	return {
		baseUrl,

		// ---- Account
		getMe: (signal?: AbortSignal) => json<CloudMe>("GET", "/me", { signal }),
		createOrganization: (displayName: string) =>
			json<{ organization: CloudOrganization }>("POST", "/orgs", { body: { displayName } }),

		// ---- Projects
		listProjects: (orgId: string, list: ListOptions = {}) =>
			json<CloudPage<CloudProject>>("GET", `${org(orgId)}/projects`, { query: { cursor: list.cursor, limit: list.limit ?? 100 }, signal: list.signal }),
		getProject: (orgId: string, projectId: string) =>
			json<{ project: CloudProject }>("GET", `${org(orgId)}/projects/${seg(projectId)}`),
		createStandaloneProject: (orgId: string, input: { displayName: string; config?: CloudProjectConfig }, idempotencyKey = options.newKey()) =>
			json<{ project: CloudProject }>("POST", `${org(orgId)}/projects/standalone`, { body: input, idempotencyKey }),
		createGitHubProject: (orgId: string, input: { githubRepositoryId: string; displayName?: string; config?: CloudProjectConfig }, idempotencyKey = options.newKey()) =>
			json<{ project: CloudProject }>("POST", `${org(orgId)}/github/projects`, { body: input, idempotencyKey }),
		deleteProject: (orgId: string, projectId: string) =>
			json<{ project: { id: string; deleted: boolean } }>("DELETE", `${org(orgId)}/projects/${seg(projectId)}`),

		// ---- Billing
		/** The organization's plan, its limits and usage. Any member may read it. */
		getBilling: (orgId: string, signal?: AbortSignal) =>
			json<CloudBillingSummary>("GET", `${org(orgId)}/billing`, { signal }),

		// ---- GitHub App
		listGitHubInstallations: (orgId: string) =>
			json<{ installations: CloudGitHubInstallation[] }>("GET", `${org(orgId)}/github/installations`),
		startGitHubInstallation: (orgId: string) =>
			json<{ installationUrl: string; expiresAt: string }>("POST", `${org(orgId)}/github/installations/start`),
		syncGitHubInstallation: (orgId: string, installationId: string) =>
			json<{ installation: CloudGitHubInstallation }>("POST", `${org(orgId)}/github/installations/${seg(installationId)}/sync`),
		listGitHubRepositories: (orgId: string, list: ListOptions = {}) =>
			json<CloudPage<CloudGitHubRepository>>("GET", `${org(orgId)}/github/repositories`, { query: { cursor: list.cursor, limit: list.limit ?? 100 }, signal: list.signal }),

		// ---- Sessions
		listSessions: (orgId: string, list: ListOptions & { projectId?: string } = {}) =>
			json<CloudPage<CloudSession>>("GET", `${org(orgId)}/sessions`, { query: { projectId: list.projectId, cursor: list.cursor, limit: list.limit ?? 100 }, signal: list.signal }),
		getSession: (orgId: string, sessionId: string, signal?: AbortSignal) =>
			json<{ session: CloudSession }>("GET", session(orgId, sessionId), { signal }),
		createSession: (orgId: string, input: CreateCloudSessionInput, idempotencyKey = options.newKey()) =>
			json<{ session: CloudSession }>("POST", `${org(orgId)}/sessions`, { body: input, idempotencyKey }),
		/** Archive: stops the sandbox, keeps the history. */
		/**
		 * Opens the session's event stream (server-sent events): what it already
		 * holds after `after`, then each client event as it is committed. The body
		 * streams until `signal` aborts or the control plane ends it.
		 */
		openEventStream: async (orgId: string, sessionId: string, input: { after: number; signal: AbortSignal }): Promise<Response> => {
			const token = await bearer();
			const nativeFetch = options.nativeFetch ?? doFetch;
			let response: Response;
			try {
				response = await nativeFetch(buildUrl(baseUrl, `${session(orgId, sessionId)}/events`, { after: Math.max(0, input.after) }), {
					method: "GET",
					headers: { Authorization: `Bearer ${token}`, Accept: "text/event-stream" },
					signal: input.signal,
				});
			} catch (cause) {
				if (input.signal.aborted) throw Object.assign(new Error("Request cancelled"), { name: "AbortError" });
				throw new CloudError("Couldn't reach Orchestrator Cloud.", { status: 0, cause });
			}
			if (!response.ok) {
				throw await refusal(response);
			}
			return response;
		},
		/**
		 * Uploads a file for a message to carry: its bytes, its plain file name,
		 * and their SHA-256, which the control plane checks before keeping it.
		 */
		uploadAttachment: async (orgId: string, sessionId: string, input: { name: string; bytes: Uint8Array; sha256: string; contentType: string; signal?: AbortSignal }): Promise<CloudAttachment> => {
			const token = await bearer();
			const nativeFetch = options.nativeFetch ?? doFetch;
			let response: Response;
			try {
				response = await nativeFetch(buildUrl(baseUrl, `${session(orgId, sessionId)}/attachments`, { name: input.name }), {
					method: "POST",
					headers: { Authorization: `Bearer ${token}`, Accept: "application/json", "Content-Type": input.contentType || "application/octet-stream", [ARTIFACT_SHA256_HEADER]: input.sha256 },
					body: input.bytes as unknown as BodyInit,
					signal: input.signal,
				});
			} catch (cause) {
				if (input.signal?.aborted) throw Object.assign(new Error("Request cancelled"), { name: "AbortError" });
				throw new CloudError("Couldn't reach Orchestrator Cloud.", { status: 0, cause });
			}
			if (!response.ok) {
				throw await refusal(response);
			}
			const { attachment } = JSON.parse(await response.text()) as { attachment: CloudAttachment };
			return attachment;
		},
		/** Where an attachment's bytes download from, with the header that reads them. */
		attachmentRequest: async (orgId: string, sessionId: string, attachmentId: string): Promise<{ url: string; headers: Record<string, string> }> => ({
			url: buildUrl(baseUrl, `${session(orgId, sessionId)}/attachments/${seg(attachmentId)}`),
			headers: { Authorization: `Bearer ${await bearer()}` },
		}),
		/** Retitles the session for every viewer; a viewer's own request is refused. */
		renameSession: (orgId: string, sessionId: string, displayName: string) =>
			json<{ session: CloudSession }>("PATCH", session(orgId, sessionId), { body: { displayName } }),
		/** Pins the session for the signed-in person only. */
		pinSession: (orgId: string, sessionId: string) =>
			json<CloudSessionPin>("POST", `${session(orgId, sessionId)}/pin`),
		unpinSession: (orgId: string, sessionId: string) =>
			json<CloudSessionPin>("DELETE", `${session(orgId, sessionId)}/pin`),
		deleteSession: (orgId: string, sessionId: string) =>
			json<{ session: { id: string; desiredState?: string } }>("DELETE", session(orgId, sessionId)),
		restoreSession: (orgId: string, sessionId: string) =>
			json<{ session: { id: string } }>("POST", `${session(orgId, sessionId)}/restore`),
		/** Wake a paused sandbox. */
		resumeSession: (orgId: string, sessionId: string) =>
			json<{ session: { id: string; desiredState?: string; observedState?: string } }>("POST", `${session(orgId, sessionId)}/resume`),
		/**
		 * Start the agent again inside its live sandbox, after it exited or
		 * crashed: the desktop's Resume agent. `requested` is false when the
		 * worker found it already running.
		 */
		restartSessionAgent: (orgId: string, sessionId: string) =>
			json<{ requested: boolean }>("POST", `${session(orgId, sessionId)}/agent/restart`, { timeoutMs: WORKER_TIMEOUT_MS }),
		/** Heartbeat while a session is on screen, so reading keeps it from idle-pausing. */
		touchPresence: (orgId: string, sessionId: string) => send("POST", `${session(orgId, sessionId)}/presence`).then(() => undefined),
		retryStartup: (orgId: string, sessionId: string) =>
			json<{ session: CloudSession }>("POST", `${session(orgId, sessionId)}/startup-retry`),

		// ---- Switching between Chat and Terminal UI
		getInterfaceTransition: (orgId: string, sessionId: string) =>
			json<CloudInterfaceTransitionStatus>("GET", `${session(orgId, sessionId)}/interface-transition`),
		/** `model` and `reasoningEffort` start a Chat session's Terminal UI on what its chat had chosen (Chat to terminal only). */
		startInterfaceTransition: (orgId: string, sessionId: string, input: { targetMode: "chat" | "tui"; policy: "drain" | "interrupt"; model?: string; reasoningEffort?: string }, idempotencyKey = options.newKey()) =>
			json<{ transition: CloudInterfaceTransition }>("POST", `${session(orgId, sessionId)}/interface-transition`, { body: input, idempotencyKey }),
		cancelInterfaceTransition: (orgId: string, sessionId: string) =>
			json<{ ok: boolean }>("DELETE", `${session(orgId, sessionId)}/interface-transition`),
		acknowledgeInterfaceTransitionNotice: (orgId: string, sessionId: string, transitionId: string) =>
			json<{ ok: boolean }>("PUT", `${session(orgId, sessionId)}/interface-transition/${seg(transitionId)}/notice-acknowledgement`),

		// ---- Chat
		/**
		 * A page of the chat's events: forward from `after`, or, with `before`,
		 * the newest below it (see chatHistory.ts); `since` narrows a `before`
		 * read to events created at or after a time.
		 */
		listChatEvents: (orgId: string, sessionId: string, query: { after?: number; before?: number; since?: string; limit?: number; signal?: AbortSignal }) =>
			json<CloudChatEventsPage>("GET", `${session(orgId, sessionId)}/chat-events`, { query: { after: query.after, before: query.before, since: query.since, limit: query.limit ?? 500 }, signal: query.signal }),
		sendMessage: (orgId: string, sessionId: string, input: CloudSendMessageInput, idempotencyKey = options.newKey()) =>
			json<{ event: unknown }>("POST", `${session(orgId, sessionId)}/messages`, { body: input, idempotencyKey }),
		cancelTurn: (orgId: string, sessionId: string, turnId: string) =>
			json<{ ok: boolean }>("POST", `${session(orgId, sessionId)}/turns/${seg(turnId)}/cancel`, { idempotencyKey: options.newKey() }),
		steerTurn: (orgId: string, sessionId: string, turnId: string, text: string, idempotencyKey = options.newKey()) =>
			json<{ event: unknown }>("POST", `${session(orgId, sessionId)}/turns/${seg(turnId)}/steer`, { body: { text }, idempotencyKey }),
		decideApproval: (orgId: string, sessionId: string, requestId: string, decisionId: string) =>
			json<{ ok: boolean }>("POST", `${session(orgId, sessionId)}/approvals/${seg(requestId)}/decide`, { body: { decisionId } }),
		listChatModels: (orgId: string, sessionId: string) =>
			json<CloudChatModels>("GET", `${session(orgId, sessionId)}/chat-models`, { timeoutMs: WORKER_TIMEOUT_MS }),
		/** The models a new task can pick for an agent, before any session exists. */
		listAgentModels: (orgId: string, agent: string) =>
			json<CloudChatModels>("GET", `${org(orgId)}/providers/${seg(agent)}/models`, { timeoutMs: WORKER_TIMEOUT_MS }),

		/** Reviewer agent, auto review, and whether review comments and CI failures go to the agent. */
		updateSessionPreferences: (orgId: string, sessionId: string, preferences: CloudSessionPreferences) =>
			json<{ session: CloudSession }>("PATCH", `${session(orgId, sessionId)}/preferences`, { body: preferences }),

		// ---- Pull requests and AO reviews
		listSessionPullRequests: (orgId: string, sessionId: string, signal?: AbortSignal) =>
			json<CloudSessionPullRequests>("GET", `${session(orgId, sessionId)}/pull-requests`, { signal }),
		/** Squash-merges, fenced to the head commit on screen so a newer push is never merged unseen. */
		mergePullRequest: (orgId: string, sessionId: string, number: number, prUrl: string, expectedHeadSha: string) =>
			json<{ status: string }>("POST", `${session(orgId, sessionId)}/pull-requests/${seg(String(number))}/merge`, { body: { prUrl, expectedHeadSha }, timeoutMs: WORKER_TIMEOUT_MS }),
		getSessionReviewState: (orgId: string, sessionId: string, signal?: AbortSignal) =>
			json<CloudSessionReviewState>("GET", `${session(orgId, sessionId)}/reviews`, { signal }),
		/** Starts a review of every open pull request; `created` is false when each head was already reviewed. */
		triggerSessionReviews: async (orgId: string, sessionId: string): Promise<CloudSessionReviewState & { created: boolean }> => {
			const response = await send("POST", `${session(orgId, sessionId)}/reviews/trigger`, { timeoutMs: WORKER_TIMEOUT_MS });
			const text = await response.text();
			return { ...(JSON.parse(text || "{}") as CloudSessionReviewState), created: response.status === 201 };
		},
		cancelSessionReviews: (orgId: string, sessionId: string) =>
			json<CloudSessionReviewState>("POST", `${session(orgId, sessionId)}/reviews/cancel`),
		/** Sends a stored review run to the worker as the control plane writes it, and records that it went. */
		sendReviewToWorker: (orgId: string, sessionId: string, reviewRunId: string, idempotencyKey = options.newKey()) =>
			json<{ event: unknown }>("POST", `${session(orgId, sessionId)}/reviews/${seg(reviewRunId)}/send`, { idempotencyKey }),
		/** A review that runs in Chat: its conversation (review.* events), the person's messages, and stop. */
		listReviewChatEvents: (orgId: string, sessionId: string, reviewRunId: string, query: { after: number; limit?: number; signal?: AbortSignal }) =>
			json<CloudChatEventsPage>("GET", `${session(orgId, sessionId)}/reviews/${seg(reviewRunId)}/chat-events`, { query: { after: query.after, limit: query.limit ?? 500 }, signal: query.signal }),
		sendReviewChatMessage: (orgId: string, sessionId: string, reviewRunId: string, text: string, idempotencyKey = options.newKey()) =>
			json<{ event: unknown }>("POST", `${session(orgId, sessionId)}/reviews/${seg(reviewRunId)}/chat/messages`, { body: { text }, idempotencyKey }),
		interruptReviewChat: (orgId: string, sessionId: string, reviewRunId: string) =>
			send("POST", `${session(orgId, sessionId)}/reviews/${seg(reviewRunId)}/chat/interrupt`).then(() => undefined),

		// ---- The session's workspace, read through its worker (which a read wakes)
		getWorkspaceReview: (orgId: string, sessionId: string, signal?: AbortSignal) =>
			json<CloudWorkspaceReview>("GET", `${session(orgId, sessionId)}/workspace/review`, { signal, timeoutMs: WORKER_TIMEOUT_MS }),
		getWorkspaceReviewFile: (orgId: string, sessionId: string, query: { path: string; scope?: CloudWorkspaceScope }, signal?: AbortSignal) =>
			json<CloudWorkspaceFile>("GET", `${session(orgId, sessionId)}/workspace/review/file`, { query: { path: query.path, scope: query.scope }, signal, timeoutMs: WORKER_TIMEOUT_MS }),
		getWorkspaceTree: (orgId: string, sessionId: string, path: string, signal?: AbortSignal) =>
			json<CloudWorkspaceTree>("GET", `${session(orgId, sessionId)}/workspace/tree`, { query: { path: path || undefined }, signal, timeoutMs: WORKER_TIMEOUT_MS }),
		searchWorkspace: (orgId: string, sessionId: string, query: { query: string; limit?: number }, signal?: AbortSignal) =>
			json<CloudWorkspaceSearch>("GET", `${session(orgId, sessionId)}/workspace/search`, { query: { query: query.query, limit: query.limit }, signal, timeoutMs: WORKER_TIMEOUT_MS }),
		getWorkspaceRevision: (orgId: string, sessionId: string, query: { path: string; side: "before" | "after"; workspaceVersion?: string; scope?: CloudWorkspaceScope }, signal?: AbortSignal) =>
			json<CloudWorkspaceRevision>("GET", `${session(orgId, sessionId)}/workspace/review/revision`, { query, signal, timeoutMs: WORKER_TIMEOUT_MS }),

		// ---- Artifacts (present where the organization's offering has them)
		listSessionArtifacts: (orgId: string, sessionId: string, signal?: AbortSignal) =>
			json<{ artifacts: CloudArtifact[] }>("GET", `${session(orgId, sessionId)}/artifacts`, { signal }),
		/** The person's public links to files; 404 where the control plane serves none. */
		listArtifactShares: (signal?: AbortSignal) =>
			json<CloudArtifactShareList>("GET", "/me/artifact-shares", { signal }),
		/** The signed prefix the session's artifact files load from, with no header. */
		createArtifactAccess: (orgId: string, sessionId: string) =>
			json<CloudArtifactAccess>("POST", `${session(orgId, sessionId)}/artifacts/access`),
		/** Deletes one artifact file; the session's creator only. */
		deleteSessionArtifact: (orgId: string, sessionId: string, path: string) =>
			send("DELETE", `${session(orgId, sessionId)}/artifacts`, { query: { path } }).then(() => undefined),
		listArtifactVersions: (orgId: string, sessionId: string, path: string, signal?: AbortSignal) =>
			json<{ versions: CloudArtifactVersionSummary[] }>("GET", `${session(orgId, sessionId)}/artifacts/versions`, { query: { path }, signal }),
		getArtifactVersion: (orgId: string, sessionId: string, path: string, version: number, signal?: AbortSignal) =>
			json<CloudArtifactVersionSummary & { path: string; content: string; truncated: boolean }>("GET", `${session(orgId, sessionId)}/artifacts/versions/${seg(String(version))}`, { query: { path }, signal }),
		/** Rewrites the file with an earlier version, keeping the one it replaces; the session's creator only. */
		restoreArtifactVersion: (orgId: string, sessionId: string, path: string, version: number) =>
			json<{ artifact: CloudArtifact }>("POST", `${session(orgId, sessionId)}/artifacts/versions/restore`, { body: { path, version } }),

		// ---- Terminal
		createTerminalTicket: (orgId: string, sessionId: string, kind: CloudTerminalKind) =>
			json<CloudTerminalTicket>("POST", `${session(orgId, sessionId)}/terminal-ticket`, { body: { kind }, timeoutMs: WORKER_TIMEOUT_MS }),
		/**
		 * The WebSocket address a ticket opens. Built by hand: React Native's URL
		 * polyfill has no working protocol setter. The single-use ticket is the
		 * only credential; the account token never goes on a WebSocket URL.
		 */
		terminalUrl: (ticket: string, kind: CloudTerminalKind, after = 0) =>
			`${cloudWebSocketBase(baseUrl)}${CLOUD_API_PREFIX}/terminal?ticket=${encodeURIComponent(ticket)}&kind=${kind}&after=${after}&protocol=2`,

		// ---- Personal coding-agent credentials
		listProviderConnections: () =>
			json<{ providerConnections: CloudProviderConnection[] }>("GET", "/me/providers"),
		putProviderConnection: (agent: string, credentialType: string, secret: string) =>
			json<{ providerConnection: CloudProviderConnection }>("PUT", `/me/providers/${seg(agent)}`, { body: { credentialType, secret }, timeoutMs: 30_000 }),
		deleteProviderConnection: (agent: string) => send("DELETE", `/me/providers/${seg(agent)}`).then(() => undefined),

		// ---- Notifications
		listNotifications: (orgId: string, query: { status?: "unread" | "read" | "all"; cursor?: string; limit?: number } = {}) =>
			json<CloudNotificationPage>("GET", `${org(orgId)}/notifications`, { query: { status: query.status ?? "all", cursor: query.cursor, limit: query.limit ?? 50 } }),
		markNotificationRead: (orgId: string, notificationId: string) =>
			json<{ updated: number }>("PATCH", `${org(orgId)}/notifications/${seg(notificationId)}`, { body: { status: "read" } }),
		markAllNotificationsRead: (orgId: string) =>
			json<{ updated: number }>("POST", `${org(orgId)}/notifications/read-all`),

		// ---- Push devices: Orchestrator Cloud sends this phone its notifications.
		putPushDevice: (installId: string, input: { token: string; platform: string; deviceName?: string }) =>
			send("PUT", `/me/push-devices/${seg(installId)}`, { body: input }).then(() => undefined),
		deletePushDevice: (installId: string) =>
			send("DELETE", `/me/push-devices/${seg(installId)}`, { timeoutMs: 5_000 }).then(() => undefined),
	};
}

export type CloudClient = ReturnType<typeof createCloudClient>;

/** Page through a cursor-paginated list until it ends, with a ceiling against a server that never does. */
export async function collectPages<T>(fetchPage: (cursor?: string) => Promise<CloudPage<T>>, maxPages = 20): Promise<T[]> {
	const all: T[] = [];
	let cursor: string | undefined;
	for (let page = 0; page < maxPages; page++) {
		const result = await fetchPage(cursor);
		all.push(...result.items);
		if (!result.page?.hasMore || !result.page.nextCursor) return all;
		cursor = result.page.nextCursor;
	}
	return all;
}
