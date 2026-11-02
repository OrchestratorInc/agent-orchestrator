import { beforeEach, describe, expect, it, vi } from "vitest";

const captured: Array<{ event: string; properties?: Record<string, unknown> }> = [];
const contexts: Array<Record<string, unknown>> = [];

vi.mock("./telemetry", () => ({
	captureRendererEvent: vi.fn(async (event: string, properties?: Record<string, unknown>) => {
		captured.push({ event, properties });
	}),
	setCloudTelemetryContext: vi.fn((context: Record<string, unknown>) => {
		contexts.push(context);
	}),
}));

import type { CloudCpClient } from "./cloud-cp/client";
import { CloudCpError } from "./cloud-cp/errors";
import type { CloudCpMeResponse, CloudCpSession } from "./cloud-cp/types";
import { createCloudSession } from "./cloud-session-create";
import {
	instrumentCloudClient,
	noteCloudSession,
	observeCloudSessions,
	reportCloudOffering,
	reportPlanRefusal,
	resetCloudTelemetryForTest,
} from "./cloud-telemetry";

function session(overrides: Partial<CloudCpSession> = {}): CloudCpSession {
	return {
		id: "session-1",
		orgId: "org-1",
		projectId: "project-1",
		kind: "worker",
		harness: "claude-code",
		displayName: "Fix the bug",
		branch: "main",
		mode: "trusted",
		interfaceMode: "tui",
		deniedCommands: [],
		activityState: "idle",
		status: "working",
		runtimeConnected: false,
		sandboxProvider: "freestyle",
		observedState: "provisioning",
		isTerminated: false,
		prs: [],
		createdAt: new Date().toISOString(),
		...overrides,
	} as CloudCpSession;
}

function fakeClient(overrides: Partial<CloudCpClient> = {}): CloudCpClient {
	return {
		me: vi.fn(async () => ({ user: { id: "u", email: "a@b.c", displayName: "A", authProvider: "workos" }, organizations: [] })),
		createSession: vi.fn(async () => ({ session: session() })),
		listSessions: vi.fn(async () => ({ items: [], page: { hasMore: false } })),
		getSession: vi.fn(async () => ({ session: session() })),
		createGitHubProject: vi.fn(async () => ({ project: { id: "project-9" } })),
		createBillingCheckout: vi.fn(async () => ({ url: "https://checkout.stripe.test" })),
		getBilling: vi.fn(async () => ({ enabled: true, exempt: false, plan: "free", entitled: false })),
		...overrides,
	} as unknown as CloudCpClient;
}

const events = () => captured.map((entry) => entry.event);

beforeEach(() => {
	captured.length = 0;
	contexts.length = 0;
	resetCloudTelemetryForTest();
	window.sessionStorage.clear();
});

describe("cloud funnel telemetry", () => {
	it("reports a session request, then ready with time-to-ready from the first connected snapshot", async () => {
		const client = instrumentCloudClient(fakeClient());
		const { session: created } = await createCloudSession(client, "org-1", {
			projectId: "project-1", kind: "worker", harness: "claude-code", displayName: "x", prompt: "secret prompt", provider: "freestyle",
			interfaceMode: "tui",
		});
		expect(created.id).toBe("session-1");
		expect(events()).toEqual(["ao.renderer.cloud_session_create_requested"]);
		expect(JSON.stringify(captured)).not.toContain("secret prompt");

		observeCloudSessions([session({ observedState: "bootstrapping" })]);
		expect(events()).toHaveLength(1);
		observeCloudSessions([session({ runtimeConnected: true, observedState: "running" })], Date.now() + 4200);
		observeCloudSessions([session({ runtimeConnected: true, observedState: "running" })], Date.now() + 9000);
		expect(events()).toEqual(["ao.renderer.cloud_session_create_requested", "ao.renderer.cloud_session_ready"]);
		const ready = captured[1]?.properties ?? {};
		expect(ready.provider).toBe("freestyle");
		expect(ready.ttr_ms).toBeGreaterThanOrEqual(4000);
	});

	it("reports a startup the control plane gave up on, and a refused create, without changing the error", async () => {
		const refusal = new CloudCpError("Choose a plan", { status: 402, code: "PLAN_REQUIRED" });
		const client = instrumentCloudClient(fakeClient({
			createSession: vi.fn(async () => {
				throw refusal;
			}),
		} as Partial<CloudCpClient>));
		await expect(client.createSession("org-1", {
			projectId: "p", kind: "orchestrator", harness: "codex", displayName: "o", prompt: "",
		})).rejects.toBe(refusal);
		expect(captured.at(-1)).toMatchObject({
			event: "ao.renderer.cloud_session_failed",
			properties: { stage: "create", code: "PLAN_REQUIRED", status: 402, session_kind: "orchestrator" },
		});

		const startup = instrumentCloudClient(fakeClient());
		await startup.createSession("org-1", { projectId: "p", kind: "worker", harness: "codex", displayName: "w", prompt: "" });
		await startup.listSessions("org-1");
		observeCloudSessions([session({ runtimeState: "terminated", startupError: { code: "bootstrap_failed", message: "m", at: "" } })]);
		expect(captured.at(-1)).toMatchObject({
			event: "ao.renderer.cloud_session_failed",
			properties: { stage: "startup", code: "bootstrap_failed" },
		});
	});

	it("does not report the older control plane's rejection of the default interface, which the create retries", async () => {
		const rejection = new CloudCpError("The request body is invalid.", { status: 400, code: "invalid_request" });
		const createSession = vi.fn(async (_orgId: string, body: { interfaceMode?: string }) => {
			if (body.interfaceMode !== undefined) throw rejection;
			return { session: session() };
		});
		const client = instrumentCloudClient(fakeClient({ createSession } as unknown as Partial<CloudCpClient>));
		const body = { projectId: "p", kind: "worker" as const, harness: "claude-code", displayName: "w", prompt: "" };
		await createCloudSession(client, "org-1", { ...body, interfaceMode: "chat" });
		expect(createSession).toHaveBeenCalledTimes(2);
		expect(events()).not.toContain("ao.renderer.cloud_session_failed");
		// One user create is one request, however many calls it takes.
		expect(events().filter((event) => event === "ao.renderer.cloud_session_create_requested")).toHaveLength(1);

		// The same 400 without the field is a real failure.
		const invalid = instrumentCloudClient(fakeClient({
			createSession: vi.fn(async () => {
				throw rejection;
			}),
		} as Partial<CloudCpClient>));
		await expect(invalid.createSession("org-1", body)).rejects.toBe(rejection);
		expect(captured.at(-1)).toMatchObject({
			event: "ao.renderer.cloud_session_failed",
			properties: { stage: "create", code: "invalid_request", status: 400 },
		});
	});

	it("counts a sign-in only when a new user signs in, not on mount or token refresh", () => {
		const account = { authProvider: "workos", user: { id: "user-1" } };
		noteCloudSession(null, true);
		noteCloudSession(account, false);
		noteCloudSession(account, false);
		noteCloudSession(account, true);
		expect(events()).toEqual(["ao.renderer.cloud_signed_in"]);
		noteCloudSession(null, false);
		noteCloudSession(account, false);
		expect(events()).toEqual(["ao.renderer.cloud_signed_in", "ao.renderer.cloud_signed_in"]);

		resetCloudTelemetryForTest();
		captured.length = 0;
		noteCloudSession(account, true);
		expect(events()).toEqual([]);
	});

	it("records the offering from /me once and tolerates a control plane without it", async () => {
		const me = {
			user: { id: "u", email: "a@b.c", displayName: "A", authProvider: "workos" },
			organizations: [{ id: "org-1", slug: "o", displayName: "O", role: "owner" }],
			sandboxProviders: { available: ["freestyle"], default: "freestyle" },
			offering: { providers: ["freestyle"], defaultProvider: "freestyle", billingMode: "self_serve", kind: "b2c", features: [] },
		} as unknown as CloudCpMeResponse;
		const client = instrumentCloudClient(fakeClient({ me: vi.fn(async () => me) } as Partial<CloudCpClient>));
		await client.me();
		await client.me();
		expect(events()).toEqual(["ao.renderer.cloud_offering_resolved"]);
		expect(captured[0]?.properties).toMatchObject({ org_kind: "b2c", billing_mode: "self_serve", default_provider: "freestyle", provider_count: 1 });
		expect(contexts.at(-1)).toEqual({ orgId: "org-1", orgKind: "b2c", billingMode: "self_serve" });

		reportCloudOffering({ ...me, offering: undefined } as unknown as CloudCpMeResponse);
		expect(captured.at(-1)?.properties).toMatchObject({ org_kind: "unknown", billing_mode: "unknown", default_provider: "freestyle" });

		// A project created without an explicit provider reports the org default.
		await client.createGitHubProject("org-1", { githubRepositoryId: "42", displayName: "acme/secret-repo" });
		expect(captured.at(-1)).toMatchObject({ event: "ao.renderer.cloud_project_created", properties: { provider: "freestyle", project_id: "project-9" } });
		expect(JSON.stringify(captured.at(-1))).not.toContain("secret-repo");
	});

	it("splits plan refusals from limits", () => {
		reportPlanRefusal("PLAN_REQUIRED", true);
		reportPlanRefusal("WEEKLY_LIMIT_REACHED", false);
		expect(captured).toEqual([
			{ event: "ao.renderer.cloud_plan_refusal_shown", properties: { code: "PLAN_REQUIRED", shown: true } },
			{ event: "ao.renderer.cloud_limit_reached", properties: { code: "WEEKLY_LIMIT_REACHED", shown: false } },
		]);
	});

	it("reports checkout completion once the pending checkout's subscription is active", async () => {
		let summary = { enabled: true, exempt: false, plan: "free", entitled: false } as Record<string, unknown>;
		const client = instrumentCloudClient(fakeClient({ getBilling: vi.fn(async () => summary) } as unknown as Partial<CloudCpClient>));
		await client.getBilling("org-1");
		await client.createBillingCheckout("org-1", "starter");
		await client.getBilling("org-1");
		expect(events()).toEqual(["ao.renderer.cloud_checkout_started"]);
		summary = { enabled: true, exempt: false, plan: "starter", entitled: true, subscriptionStatus: "active" };
		await client.getBilling("org-1");
		await client.getBilling("org-1");
		expect(events()).toEqual(["ao.renderer.cloud_checkout_started", "ao.renderer.cloud_checkout_completed"]);
		expect(captured[1]?.properties).toMatchObject({ plan: "starter" });
	});

	it("passes other client methods through untouched", async () => {
		const deleteSession = vi.fn(async () => undefined);
		const client = instrumentCloudClient(fakeClient({ deleteSession } as unknown as Partial<CloudCpClient>));
		await client.deleteSession("org-1", "session-1");
		expect(deleteSession).toHaveBeenCalledWith("org-1", "session-1");
		expect(events()).toEqual([]);
	});
});
