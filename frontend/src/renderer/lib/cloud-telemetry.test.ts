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
	hashedTelemetryID: vi.fn(async (value: unknown) => (typeof value === "string" && value ? `hash:${value}` : undefined)),
}));

import type { CloudCpClient } from "./cloud-cp/client";
import { CloudCpError } from "./cloud-cp/errors";
import type { CloudCpMeResponse, CloudCpSession } from "./cloud-cp/types";
import { createCloudSession } from "./cloud-session-create";
import {
	clearSignInSource,
	instrumentCloudClient,
	noteCloudSession,
	noteCloudSignInCompleted,
	noteSignInSource,
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
	window.localStorage.clear();
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
		expect(captured.map((entry) => entry.properties?.same_account)).toEqual([false, false]);

		resetCloudTelemetryForTest();
		captured.length = 0;
		noteCloudSession(account, true);
		expect(events()).toEqual([]);
	});

	it("counts signing in again as the same account once, from main's completed sign-in", () => {
		const first = { authProvider: "workos", user: { id: "user-1" }, storedAt: "2026-11-16T10:00:00Z" };
		const again = { ...first, storedAt: "2026-11-16T10:20:00Z" };
		noteCloudSession(first, true);
		// A link refused the expired session; Publish's Sign in signs the same account in again.
		noteSignInSource("artifact_publish");
		noteCloudSession(again, false);
		expect(events()).toEqual([]);
		noteCloudSignInCompleted(again);
		expect(captured).toEqual([{ event: "ao.renderer.cloud_signed_in", properties: { method: "workos", source: "artifact_publish", same_account: true } }]);

		// A new account: the notification reports it, and main's completed sign-in does not again.
		const other = { authProvider: "workos", user: { id: "user-2" }, storedAt: "2026-11-16T11:00:00Z" };
		noteCloudSession(other, false);
		noteCloudSignInCompleted(other);
		expect(events()).toEqual(["ao.renderer.cloud_signed_in", "ao.renderer.cloud_signed_in"]);

		// In the other order, the notification does not count it again either.
		const third = { authProvider: "workos", user: { id: "user-3" }, storedAt: "2026-11-16T12:00:00Z" };
		noteCloudSignInCompleted(third);
		noteCloudSession(third, false);
		expect(events()).toHaveLength(3);
		// Only the re-sign-in is marked, so same_account != true is the count before it was added.
		expect(captured.map((entry) => entry.properties?.same_account)).toEqual([true, false, false]);
	});

	it("says a sign-in came from Publish only within 30 minutes of its Sign in click, once", () => {
		const account = { authProvider: "workos", user: { id: "user-1" } };
		const sources = () => captured.map((entry) => entry.properties?.source);
		noteCloudSession(null, true);
		noteSignInSource("artifact_publish");
		noteCloudSession(account, false);
		// The note is used up: the next sign-in is not Publish's.
		noteCloudSession(null, false);
		noteCloudSession(account, false);
		expect(sources()).toEqual(["artifact_publish", "other"]);

		// Expired.
		noteCloudSession(null, false);
		noteSignInSource("artifact_publish", Date.now() - 31 * 60_000);
		noteCloudSession(account, false);
		// Cleared when the popover that set it closed.
		noteCloudSession(null, false);
		noteSignInSource("artifact_publish");
		clearSignInSource("artifact_publish");
		noteCloudSession(account, false);
		expect(sources()).toEqual(["artifact_publish", "other", "other", "other"]);
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
		expect(contexts.at(-1)).toEqual({ orgId: "org-1", orgKind: "b2c", billingMode: "self_serve", plan: "unknown", paid: false, trial: false });

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
		reportPlanRefusal("WORKER_SLOTS_FULL", false, "chat");
		expect(captured).toEqual([
			{ event: "ao.renderer.cloud_plan_refusal_shown", properties: { code: "PLAN_REQUIRED", shown: true } },
			{ event: "ao.renderer.cloud_limit_reached", properties: { code: "WEEKLY_LIMIT_REACHED", shown: false } },
			{ event: "ao.renderer.cloud_limit_reached", properties: { code: "WORKER_SLOTS_FULL", shown: false, source: "chat" } },
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
		expect(events()).toEqual(["ao.renderer.cloud_checkout_started", "ao.renderer.cloud_checkout_completed", "ao.billing.plan_changed"]);
		expect(captured[1]?.properties).toMatchObject({ plan: "starter" });
	});

	it("tells a free trial apart from a paid checkout", async () => {
		const plans = [{ name: "starter", id: "price_1", trialDays: 7 }, { name: "pro", id: "price_2" }];
		let summary = { enabled: true, exempt: false, plan: "free", entitled: false, plans } as Record<string, unknown>;
		const client = instrumentCloudClient(fakeClient({ getBilling: vi.fn(async () => summary) } as unknown as Partial<CloudCpClient>));
		await client.getBilling("org-1");
		await client.createBillingCheckout("org-1", "starter");
		expect(captured.at(-1)).toMatchObject({ event: "ao.renderer.cloud_checkout_started", properties: { plan: "starter", trial: true } });
		summary = { enabled: true, exempt: false, plan: "starter", entitled: true, subscriptionStatus: "trialing", plans: [plans[1]] };
		await client.getBilling("org-1");
		expect(captured.at(-2)).toMatchObject({ event: "ao.renderer.cloud_checkout_completed", properties: { plan: "starter", trial: true } });
		// The trial is used up: a later checkout of another plan is paid.
		await client.createBillingCheckout("org-1", "pro");
		expect(captured.at(-1)).toMatchObject({ event: "ao.renderer.cloud_checkout_started", properties: { plan: "pro", trial: false } });
		summary = { enabled: true, exempt: false, plan: "pro", entitled: true, subscriptionStatus: "active", plans: [plans[1]] };
		await client.getBilling("org-1");
		expect(captured.at(-2)).toMatchObject({ event: "ao.renderer.cloud_checkout_completed", properties: { plan: "pro", trial: false } });
	});

	describe("plan tagging and ao.billing.plan_changed", () => {
		const paid = (plan: string) => ({ enabled: true, exempt: false, billingMode: "self_serve", plan, entitled: plan !== "free" });
		const me = {
			user: { id: "u", email: "a@b.c", displayName: "A", authProvider: "workos" },
			organizations: [{ id: "org-1", slug: "o", displayName: "O", role: "owner" }],
			offering: { providers: [], billingMode: "self_serve", kind: "b2c" },
		} as unknown as CloudCpMeResponse;
		const planChanges = () => captured.filter((entry) => entry.event === "ao.billing.plan_changed").map((entry) => entry.properties);

		function billingClient(initial: Record<string, unknown>) {
			let summary: Record<string, unknown> | Error = initial;
			const client = instrumentCloudClient(fakeClient({
				me: vi.fn(async () => me),
				getBilling: vi.fn(async () => {
					if (summary instanceof Error) throw summary;
					return summary;
				}),
			} as unknown as Partial<CloudCpClient>));
			return { client, set: (next: Record<string, unknown> | Error) => { summary = next; } };
		}

		it("reports upgrade, downgrade and cancel, but not the first load", async () => {
			const { client, set } = billingClient(paid("starter"));
			await client.getBilling("org-1");
			expect(planChanges()).toEqual([]);
			set(paid("pro"));
			await client.getBilling("org-1");
			await client.getBilling("org-1");
			set(paid("starter"));
			await client.getBilling("org-1");
			set(paid("pro"));
			await client.getBilling("org-1");
			set(paid("free"));
			await client.getBilling("org-1");
			expect(planChanges()).toEqual([
				{ from_plan: "starter", to_plan: "pro", direction: "upgrade", from_trial: false, to_trial: false },
				{ from_plan: "pro", to_plan: "starter", direction: "downgrade", from_trial: false, to_trial: false },
				{ from_plan: "starter", to_plan: "pro", direction: "upgrade", from_trial: false, to_trial: false },
				{ from_plan: "pro", to_plan: "free", direction: "cancel", from_trial: false, to_trial: false },
			]);
		});

		it("keeps the last plan across launches, per org", async () => {
			const first = billingClient(paid("starter"));
			await first.client.getBilling("org-1");
			resetCloudTelemetryForTest();
			const second = billingClient(paid("pro"));
			await second.client.getBilling("org-1");
			await second.client.getBilling("org-2");
			expect(planChanges()).toEqual([{ from_plan: "starter", to_plan: "pro", direction: "upgrade", from_trial: false, to_trial: false }]);
			// Keyed by the hashed org id (the mock's hash), never the raw id.
			expect(JSON.parse(window.localStorage.getItem("ao.telemetry.cloudPlans") ?? "{}")).toEqual({ "hash:org-1": { plan: "pro", trial: false }, "hash:org-2": { plan: "pro", trial: false } });
		});

		it("does not report a failed fetch, billing off or a sign-out", async () => {
			const { client, set } = billingClient(paid("pro"));
			await client.getBilling("org-1");
			set(new Error("network"));
			await expect(client.getBilling("org-1")).rejects.toThrow("network");
			set({ enabled: false, exempt: false, plan: "", entitled: false });
			await client.getBilling("org-1");
			noteCloudSession(null, false);
			set(paid("pro"));
			await client.getBilling("org-1");
			expect(planChanges()).toEqual([]);
		});

		it("tells a trial converting or ending apart from a paying customer", async () => {
			const trialing = (plan: string) => ({ ...paid(plan), subscriptionStatus: "trialing" });
			const { client, set } = billingClient(paid("free"));
			await client.me();
			await client.getBilling("org-1");
			// Trial starts: an upgrade into a trial; still paid, flagged as a trial.
			set(trialing("pro"));
			await client.getBilling("org-1");
			expect(contexts.at(-1)).toMatchObject({ plan: "pro", paid: true, trial: true });
			// Converts on the same plan: same_tier, no longer a trial.
			set({ ...paid("pro"), subscriptionStatus: "active" });
			await client.getBilling("org-1");
			expect(contexts.at(-1)).toMatchObject({ plan: "pro", paid: true, trial: false });
			// A second trial that ends unpaid.
			set(trialing("starter"));
			await client.getBilling("org-1");
			set(paid("free"));
			await client.getBilling("org-1");
			expect(planChanges()).toEqual([
				{ from_plan: "free", to_plan: "pro", direction: "upgrade", from_trial: false, to_trial: true },
				{ from_plan: "pro", to_plan: "pro", direction: "same_tier", from_trial: true, to_trial: false },
				{ from_plan: "pro", to_plan: "starter", direction: "downgrade", from_trial: false, to_trial: true },
				{ from_plan: "starter", to_plan: "free", direction: "cancel", from_trial: true, to_trial: false },
			]);
		});

		it("uses the active org, not the first", async () => {
			const twoOrgs = {
				...me,
				activeOrganizationId: "org-2",
				organizations: [{ id: "org-1", slug: "a", displayName: "A", role: "owner" }, { id: "org-2", slug: "b", displayName: "B", role: "owner" }],
			} as unknown as CloudCpMeResponse;
			const client = instrumentCloudClient(fakeClient({
				me: vi.fn(async () => twoOrgs),
				getBilling: vi.fn(async () => paid("pro")),
			} as unknown as Partial<CloudCpClient>));
			await client.me();
			expect(contexts.at(-1)).toMatchObject({ orgId: "org-2", plan: "unknown" });
			await client.getBilling("org-2");
			expect(contexts.at(-1)).toMatchObject({ orgId: "org-2", plan: "pro", paid: true });
		});

		it("keeps none when billing turned off while a billing request was in flight", async () => {
			let release: (summary: Record<string, unknown>) => void = () => undefined;
			const client = instrumentCloudClient(fakeClient({
				me: vi.fn(async () => me),
				getBilling: vi.fn(() => new Promise((resolve) => { release = resolve; })),
			} as unknown as Partial<CloudCpClient>));
			await client.me();
			const inFlight = client.getBilling("org-1");
			reportCloudOffering({ ...me, offering: { providers: [], billingMode: "off", kind: "b2c" } } as unknown as CloudCpMeResponse);
			release(paid("pro"));
			await inFlight;
			expect(contexts.at(-1)).toMatchObject({ plan: "none", paid: false });
		});

		it("tags the org's events with the plan once billing loads", async () => {
			const { client, set } = billingClient(paid("pro"));
			await client.me();
			expect(contexts.at(-1)).toMatchObject({ orgId: "org-1", plan: "unknown", paid: false });
			await client.getBilling("org-1");
			expect(contexts.at(-1)).toMatchObject({ orgId: "org-1", orgKind: "b2c", billingMode: "self_serve", plan: "pro", paid: true });
			// A later /me keeps the plan billing showed.
			await client.me();
			expect(contexts.at(-1)).toMatchObject({ plan: "pro", paid: true });
			// Exempt: the plan is known, but the org does not pay.
			set({ ...paid("pro"), exempt: true });
			await client.getBilling("org-1");
			expect(contexts.at(-1)).toMatchObject({ plan: "pro", paid: false });
			// Billing off on this org: none.
			reportCloudOffering({ ...me, offering: { providers: [], billingMode: "off", kind: "b2c" } } as unknown as CloudCpMeResponse);
			expect(contexts.at(-1)).toMatchObject({ plan: "none", paid: false });
		});
	});

	it("passes other client methods through untouched", async () => {
		const deleteSession = vi.fn(async () => undefined);
		const client = instrumentCloudClient(fakeClient({ deleteSession } as unknown as Partial<CloudCpClient>));
		await client.deleteSession("org-1", "session-1");
		expect(deleteSession).toHaveBeenCalledWith("org-1", "session-1");
		expect(events()).toEqual([]);
	});
});
