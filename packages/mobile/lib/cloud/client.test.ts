import { describe, expect, it, vi } from "vitest";
import { collectPages, createCloudClient } from "./client";
import { CloudAuthError, CloudError, cloudErrorMessage, isPlanRefusal, planResetCopy } from "./errors";

type Call = { url: string; init: RequestInit };

function harness(respond: (call: Call) => Response | Promise<Response>, token: string | null = "access") {
	const calls: Call[] = [];
	const onForbidden = vi.fn();
	const onPlanRefused = vi.fn();
	let keys = 0;
	const client = createCloudClient({
		baseUrl: "https://api.example.test/",
		getToken: async () => token,
		newKey: () => `key-${++keys}`,
		onForbidden,
		onPlanRefused,
		fetch: (async (url: string, init: RequestInit) => {
			const call = { url, init };
			calls.push(call);
			return respond(call);
		}) as unknown as typeof fetch,
	});
	return { client, calls, onForbidden, onPlanRefused };
}

const ok = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

describe("cloud client", () => {
	it("sends the account token and builds org-scoped paths", async () => {
		const { client, calls } = harness(() => ok({ items: [], page: { hasMore: false } }));
		await client.listSessions("org 1", { projectId: "p1", cursor: "c/2" });
		expect(calls[0].url).toBe("https://api.example.test/api/cloud/v1/orgs/org%201/sessions?projectId=p1&cursor=c%2F2&limit=100");
		expect((calls[0].init.headers as Record<string, string>).Authorization).toBe("Bearer access");
		expect(calls[0].init.method).toBe("GET");
		expect(calls[0].init.body).toBeUndefined();
	});

	it("puts an Idempotency-Key on creates and sends only the given fields", async () => {
		const { client, calls } = harness(() => ok({ session: { id: "s1" } }, 201));
		await client.createSession("o", { projectId: "p", kind: "worker", harness: "codex", displayName: "Fix it", prompt: "Fix it" });
		const headers = calls[0].init.headers as Record<string, string>;
		expect(headers["Idempotency-Key"]).toBe("key-1");
		expect(headers["Content-Type"]).toBe("application/json");
		expect(JSON.parse(String(calls[0].init.body))).toEqual({ projectId: "p", kind: "worker", harness: "codex", displayName: "Fix it", prompt: "Fix it" });
	});

	it("asks the session's switch between Chat and Terminal UI on the desktop's routes", async () => {
		const { client, calls } = harness(() => ok({ supported: true, targetMode: "tui" }));
		await client.getInterfaceTransition("o", "s");
		await client.startInterfaceTransition("o", "s", { targetMode: "tui", policy: "drain", model: "claude-opus-5-5" });
		await client.cancelInterfaceTransition("o", "s");
		await client.acknowledgeInterfaceTransitionNotice("o", "s", "t-1");
		expect(calls.map((call) => [call.init.method, call.url.replace("https://api.example.test/api/cloud/v1", "")])).toEqual([
			["GET", "/orgs/o/sessions/s/interface-transition"],
			["POST", "/orgs/o/sessions/s/interface-transition"],
			["DELETE", "/orgs/o/sessions/s/interface-transition"],
			["PUT", "/orgs/o/sessions/s/interface-transition/t-1/notice-acknowledgement"],
		]);
		expect(JSON.parse(String(calls[1].init.body))).toEqual({ targetMode: "tui", policy: "drain", model: "claude-opus-5-5" });
		expect((calls[1].init.headers as Record<string, string>)["Idempotency-Key"]).toBe("key-1");
	});

	it("reads the person's artifact share links", async () => {
		const { client, calls } = harness(() => ok({ shares: [], limit: 5, used: 0 }));
		await expect(client.listArtifactShares()).resolves.toEqual({ shares: [], limit: 5, used: 0 });
		expect([calls[0].init.method, calls[0].url]).toEqual(["GET", "https://api.example.test/api/cloud/v1/me/artifact-shares"]);
	});

	it("reuses a caller's idempotency key so a retried send is not duplicated", async () => {
		const { client, calls } = harness(() => ok({ event: {} }, 202));
		await client.sendMessage("o", "s", { text: "hi" }, "msg-1");
		await client.sendMessage("o", "s", { text: "hi" }, "msg-1");
		expect(calls.map((call) => (call.init.headers as Record<string, string>)["Idempotency-Key"])).toEqual(["msg-1", "msg-1"]);
	});

	it("surfaces the control plane's error envelope", async () => {
		const { client } = harness(() => ok({ error: "Unprocessable Entity", code: "agent_provider_required", message: "Connect codex first.", requestId: "req-9" }, 422));
		const error = await client.createSession("o", { projectId: "p", kind: "worker", harness: "codex", displayName: "x", prompt: "" }).catch((cause: unknown) => cause);
		expect(error).toBeInstanceOf(CloudError);
		expect(error).toMatchObject({ status: 422, code: "agent_provider_required", requestId: "req-9", message: "Connect codex first." });
		expect(cloudErrorMessage(error)).toMatch(/Connect this coding agent/);
	});

	it("keeps a plan refusal's reset time and says when the limit lifts", async () => {
		// Three days out from whenever this runs: a fixed date goes stale and reads as "any moment now".
		const resetsAt = new Date(Date.now() + 3 * 86_400_000).toISOString();
		const { client } = harness(() => ok({ error: "Too Many Requests", code: "WEEKLY_LIMIT_REACHED", message: "You've reached your plan's weekly usage limit.", requestId: "r", plan: "free", limit: 600, used: 600, resetsAt }, 429));
		const error = await client.resumeSession("o", "s").catch((cause: unknown) => cause);
		expect(isPlanRefusal(error)).toBe(true);
		expect(error).toMatchObject({ code: "WEEKLY_LIMIT_REACHED", resetsAt });
		expect(cloudErrorMessage(error)).toMatch(/^You've reached your plan's weekly usage limit\. Resets /);
		expect(planResetCopy(undefined)).toBeUndefined();
		expect(planResetCopy("2026-01-01T00:00:00Z", Date.parse("2026-02-01T00:00:00Z"))).toBe("It should reset any moment now.");
	});

	it("turns 401 into an auth error and reports 403 so the org can be re-resolved", async () => {
		const unauthorized = harness(() => ok({ code: "unauthorized", message: "A bearer token is required." }, 401));
		await expect(unauthorized.client.getMe()).rejects.toBeInstanceOf(CloudAuthError);
		const forbidden = harness(() => ok({ code: "forbidden", message: "Not a member." }, 403));
		await expect(forbidden.client.listProjects("o")).rejects.toMatchObject({ status: 403 });
		expect(forbidden.onForbidden).toHaveBeenCalledOnce();
	});

	// One sheet explains every plan refusal, wherever the request came from,
	// as the desktop's Upgrade page opens on any of them.
	it("tells its owner when the organization's plan refuses a request, and nothing else", async () => {
		const plan = harness(() => ok({ code: "PLAN_REQUIRED", message: "Choose a plan to use AO Cloud." }, 402));
		const error = await plan.client.createSession("o", { projectId: "p", kind: "worker", harness: "codex", displayName: "x", prompt: "x" }).catch((cause: unknown) => cause);
		expect(error).toMatchObject({ status: 402, code: "PLAN_REQUIRED" });
		expect(plan.onPlanRefused).toHaveBeenCalledExactlyOnceWith(error);

		const slots = harness(() => ok({ code: "WORKER_SLOTS_FULL", message: "All your plan's running sessions are in use." }, 409));
		const stream = await slots.client.openEventStream("o", "s", { after: 0, signal: new AbortController().signal }).catch((cause: unknown) => cause);
		expect(slots.onPlanRefused).toHaveBeenCalledExactlyOnceWith(stream);

		const conflict = harness(() => ok({ code: "conflict", message: "Already archived." }, 409));
		await conflict.client.resumeSession("o", "s").catch(() => undefined);
		expect(conflict.onPlanRefused).not.toHaveBeenCalled();
	});

	it("reads the organization's plan", async () => {
		const { client, calls } = harness(() => ok({ enabled: true, billingMode: "self_serve", plan: "free", entitled: false, exempt: false, features: [], plans: [] }));
		await expect(client.getBilling("org 1")).resolves.toMatchObject({ plan: "free", entitled: false });
		expect(calls[0].url).toBe("https://api.example.test/api/cloud/v1/orgs/org%201/billing");
		expect(calls[0].init.method).toBe("GET");
	});

	// The account token and terminal tickets would cross the network in the clear.
	it("never sends the account token to a plain-http address outside this machine", async () => {
		const calls: string[] = [];
		const getToken = vi.fn(async () => "access");
		const client = createCloudClient({
			baseUrl: "http://cloud.example.test",
			getToken,
			newKey: () => "k",
			fetch: (async (url: string) => { calls.push(url); return ok({}); }) as unknown as typeof fetch,
		});
		const error = await client.getMe().catch((cause: unknown) => cause);
		expect(error).toMatchObject({ code: "insecure_address" });
		expect(cloudErrorMessage(error)).toMatch(/https/);
		expect(getToken).not.toHaveBeenCalled();
		expect(calls).toHaveLength(0);
	});

	it("never calls the network without a token", async () => {
		const { client, calls } = harness(() => ok({}), null);
		await expect(client.getMe()).rejects.toBeInstanceOf(CloudAuthError);
		expect(calls).toHaveLength(0);
	});

	it("reports an unreachable control plane with status 0", async () => {
		const { client } = harness(() => { throw new TypeError("Network request failed"); });
		const error = await client.getMe().catch((cause: unknown) => cause);
		expect(error).toMatchObject({ status: 0 });
		expect(cloudErrorMessage(error)).toMatch(/Couldn't reach Orchestrator Cloud/);
	});

	it("accepts empty success bodies (204)", async () => {
		const { client, calls } = harness(() => new Response(null, { status: 204 }));
		await expect(client.deleteProviderConnection("codex")).resolves.toBeUndefined();
		await expect(client.touchPresence("o", "s")).resolves.toBeUndefined();
		expect(calls.map((call) => call.init.method)).toEqual(["DELETE", "POST"]);
	});

	it("builds the terminal socket address from a ticket alone", () => {
		const { client } = harness(() => ok({}));
		expect(client.terminalUrl("t+1", "agent")).toBe("wss://api.example.test/api/cloud/v1/terminal?ticket=t%2B1&kind=agent&after=0&protocol=2");
	});

	it("pages through a list until it ends", async () => {
		const pages: Record<string, { items: number[]; page: { hasMore: boolean; nextCursor?: string } }> = {
			first: { items: [1, 2], page: { hasMore: true, nextCursor: "b" } },
			b: { items: [3], page: { hasMore: false } },
		};
		await expect(collectPages(async (cursor) => pages[cursor ?? "first"])).resolves.toEqual([1, 2, 3]);
	});
});
