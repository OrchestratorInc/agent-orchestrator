import type { CloudCpClient } from "./cloud-cp/client";
import type { CloudCpCreateSessionRequest, CloudCpMeResponse, CloudCpSession } from "./cloud-cp/types";
import { captureRendererEvent, setCloudTelemetryContext } from "./telemetry";

const pendingSessions = new Map<string, { requestedAt: number; provider: string; kind: string }>();
const PENDING_SESSION_TTL_MS = 60 * 60_000;
const PENDING_CHECKOUT_KEY = "ao.telemetry.cloudCheckoutPending";

let reportedOfferings = new Set<string>();
let defaultProvider = "";

function errorCode(error: unknown): string {
	const code = (error as { code?: unknown } | null)?.code;
	return typeof code === "string" && code !== "" ? code : "unknown";
}

function errorStatus(error: unknown): number | undefined {
	const status = (error as { status?: unknown } | null)?.status;
	return typeof status === "number" ? status : undefined;
}

let knownCloudUser: string | null | undefined;

export function noteCloudSession(
	account: { authProvider: string; user: { id: string } } | null,
	initial: boolean,
): void {
	const userId = account?.user.id ?? null;
	const previous = knownCloudUser;
	knownCloudUser = userId;
	if (initial || !account || previous === userId) return;
	void captureRendererEvent("ao.renderer.cloud_signed_in", { method: account.authProvider });
}

interface OfferingShape {
	providers?: unknown;
	defaultProvider?: unknown;
	billingMode?: unknown;
	kind?: unknown;
}

export function reportCloudOffering(me: CloudCpMeResponse): void {
	const orgId = me.organizations[0]?.id;
	const offering = ((me as unknown as { offering?: OfferingShape }).offering ?? {}) as OfferingShape;
	const providers = Array.isArray(offering.providers)
		? offering.providers.filter((value): value is string => typeof value === "string")
		: me.sandboxProviders?.available ?? [];
	const fallbackDefault = typeof me.sandboxProviders?.default === "string" ? me.sandboxProviders.default : "";
	defaultProvider = typeof offering.defaultProvider === "string" ? offering.defaultProvider : fallbackDefault;
	const kind = typeof offering.kind === "string" ? offering.kind : "unknown";
	const billingMode = typeof offering.billingMode === "string" ? offering.billingMode : "unknown";
	setCloudTelemetryContext({ orgId, orgKind: kind, billingMode });
	const key = `${orgId ?? ""}:${kind}:${billingMode}:${defaultProvider}`;
	if (reportedOfferings.has(key)) return;
	reportedOfferings.add(key);
	void captureRendererEvent("ao.renderer.cloud_offering_resolved", {
		org_kind: kind,
		billing_mode: billingMode,
		default_provider: defaultProvider,
		provider_count: providers.length,
		org_count: me.organizations.length,
	});
}

export function reportUpgradeViewed(source: string): void {
	void captureRendererEvent("ao.renderer.cloud_upgrade_viewed", { source });
}

const LIMIT_CODES = new Set([
	"ORCHESTRATOR_SLOTS_FULL",
	"WORKER_SLOTS_FULL",
	"SESSION_LIMIT_REACHED",
	"WINDOW_LIMIT_REACHED",
	"WEEKLY_LIMIT_REACHED",
]);

export function reportPlanRefusal(code: string, shown: boolean): void {
	const event = LIMIT_CODES.has(code) ? "ao.renderer.cloud_limit_reached" : "ao.renderer.cloud_plan_refusal_shown";
	void captureRendererEvent(event, { code, shown });
}

function storage(): Pick<Storage, "getItem" | "setItem" | "removeItem"> | undefined {
	try {
		return window.sessionStorage;
	} catch {
		return undefined;
	}
}

function markCheckoutPending(plan: string, now: number): void {
	try {
		storage()?.setItem(PENDING_CHECKOUT_KEY, JSON.stringify({ plan, at: now }));
	} catch {
	}
}

export function observeBillingSummary(summary: { entitled?: boolean; subscriptionStatus?: string; plan?: string }): void {
	let pending: { plan?: string; at?: number } | null = null;
	try {
		const raw = storage()?.getItem(PENDING_CHECKOUT_KEY);
		pending = raw ? (JSON.parse(raw) as { plan?: string; at?: number }) : null;
	} catch {
		pending = null;
	}
	if (!pending) return;
	const status = summary.subscriptionStatus ?? "";
	if (!(summary.entitled && (status === "active" || status === "trialing"))) return;
	try {
		storage()?.removeItem(PENDING_CHECKOUT_KEY);
	} catch {
	}
	const elapsed = typeof pending.at === "number" ? Math.max(0, Date.now() - pending.at) : undefined;
	void captureRendererEvent("ao.renderer.cloud_checkout_completed", {
		plan: summary.plan ?? pending.plan ?? "unknown",
		...(elapsed === undefined ? {} : { duration_ms: elapsed }),
	});
}

function prunePending(now: number): void {
	for (const [id, entry] of pendingSessions) {
		if (now - entry.requestedAt > PENDING_SESSION_TTL_MS) pendingSessions.delete(id);
	}
}

export function observeCloudSessions(sessions: readonly CloudCpSession[], now = Date.now()): void {
	if (pendingSessions.size === 0) return;
	prunePending(now);
	for (const session of sessions) {
		const pending = pendingSessions.get(session.id);
		if (!pending) continue;
		const ttr = Math.max(0, now - pending.requestedAt);
		if (session.runtimeConnected || session.observedState === "running") {
			pendingSessions.delete(session.id);
			void captureRendererEvent("ao.renderer.cloud_session_ready", {
				provider: session.sandboxProvider ?? pending.provider,
				session_kind: pending.kind,
				ttr_ms: ttr,
			});
		} else if (session.runtimeState === "terminated" || session.isTerminated) {
			pendingSessions.delete(session.id);
			void captureRendererEvent("ao.renderer.cloud_session_failed", {
				provider: session.sandboxProvider ?? pending.provider,
				session_kind: pending.kind,
				stage: "startup",
				code: session.startupError?.code ?? "unknown",
				ttr_ms: ttr,
			});
		}
	}
}

export function resetCloudTelemetryForTest(): void {
	pendingSessions.clear();
	knownCloudUser = undefined;
	reportedOfferings = new Set();
	defaultProvider = "";
}

function projectProvider(config: Record<string, unknown> | undefined): string {
	const chosen = config?.sandboxProvider;
	if (typeof chosen === "string" && chosen !== "") return chosen;
	return defaultProvider || "default";
}

export function reportCloudSessionCreateRequested(body: Pick<CloudCpCreateSessionRequest, "provider" | "kind" | "projectId">): void {
	void captureRendererEvent("ao.renderer.cloud_session_create_requested", {
		provider: body.provider || defaultProvider || "default",
		session_kind: body.kind,
		project_id: body.projectId,
	});
}

export function instrumentCloudClient(client: CloudCpClient): CloudCpClient {
	const instrumented: CloudCpClient = { ...client };
	instrumented.me = async (options) => {
		const me = await client.me(options);
		try {
			reportCloudOffering(me);
		} catch {
		}
		return me;
	};
	instrumented.createGitHubProject = async (orgId, body, options) => {
		const response = await client.createGitHubProject(orgId, body, options);
		void captureRendererEvent("ao.renderer.cloud_project_created", {
			project_id: response.project.id,
			provider: projectProvider(body.config),
		});
		return response;
	};
	instrumented.createStandaloneProject = async (orgId, body, options) => {
		const response = await client.createStandaloneProject(orgId, body, options);
		void captureRendererEvent("ao.renderer.cloud_project_created", {
			project_id: response.project.id,
			provider: projectProvider(body.config),
		});
		return response;
	};
	instrumented.createSession = async (orgId, body, options) => {
		const requestedAt = Date.now();
		const provider = body.provider || defaultProvider || "default";
		try {
			const response = await client.createSession(orgId, body, options);
			pendingSessions.set(response.session.id, {
				requestedAt,
				provider: response.session.sandboxProvider ?? provider,
				kind: body.kind,
			});
			observeCloudSessions([response.session]);
			return response;
		} catch (error) {
			if ((body.interfaceMode !== undefined || body.defaultInterfaceMode !== undefined) && errorStatus(error) === 400 &&
				errorCode(error) === "invalid_request") {
				throw error;
			}
			void captureRendererEvent("ao.renderer.cloud_session_failed", {
				provider,
				session_kind: body.kind,
				stage: "create",
				code: errorCode(error),
				status: errorStatus(error),
				ttr_ms: Math.max(0, Date.now() - requestedAt),
			});
			throw error;
		}
	};
	instrumented.createStandaloneAgent = async (orgId, body, options) => {
		const requestedAt = Date.now();
		const provider = body.provider || defaultProvider || "default";
		try {
			const response = await client.createStandaloneAgent(orgId, body, options);
			pendingSessions.set(response.session.id, {
				requestedAt,
				provider: response.session.sandboxProvider ?? provider,
				kind: "worker",
			});
			observeCloudSessions([response.session]);
			return response;
		} catch (error) {
			void captureRendererEvent("ao.renderer.cloud_session_failed", {
				provider,
				session_kind: "worker",
				stage: "create",
				code: errorCode(error),
				status: errorStatus(error),
				ttr_ms: Math.max(0, Date.now() - requestedAt),
			});
			throw error;
		}
	};
	instrumented.listSessions = async (orgId, query, options) => {
		const response = await client.listSessions(orgId, query, options);
		observeCloudSessions(response.items);
		return response;
	};
	instrumented.getSession = async (orgId, sessionId, options) => {
		const response = await client.getSession(orgId, sessionId, options);
		observeCloudSessions([response.session]);
		return response;
	};
	instrumented.createBillingCheckout = async (orgId, plan, options) => {
		const response = await client.createBillingCheckout(orgId, plan, options);
		markCheckoutPending(plan, Date.now());
		void captureRendererEvent("ao.renderer.cloud_checkout_started", { plan });
		return response;
	};
	instrumented.getBilling = async (orgId, options) => {
		const summary = await client.getBilling(orgId, options);
		observeBillingSummary(summary);
		return summary;
	};
	return instrumented;
}
