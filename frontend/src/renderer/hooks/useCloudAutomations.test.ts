import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp";
import { cloudAutomationActiveRun, cloudAutomationErrorMessage, cloudAutomationPollInterval, cloudRunErrorMessage, cloudRunsPollInterval, useCloudAutomations, useRunCloudAutomationNow, type CloudAutomation, type CloudAutomationRun } from "./useCloudAutomations";

const mocks = vi.hoisted(() => ({
	client: {
		me: vi.fn(),
		listAutomations: vi.fn(),
		listAutomationRuns: vi.fn(),
		runAutomationNow: vi.fn(),
	},
}));

// The desktop's own account id is the identity provider's (WorkOS); the
// control plane names people by its own user ids.
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ client: mocks.client, ready: true, baseUrl: "https://cp.test", userId: "user_01WORKOS" }) }));
vi.mock("./useCloudOffering", () => ({ useCloudOffering: () => ({ ready: true, org: { id: "org-1" }, features: ["automations"] }) }));

const listKey = ["cloud-automations", "https://cp.test", "org-1"];

const iso = (ms: number) => new Date(ms).toISOString();
const run = (status: string, overrides: Partial<CloudAutomationRun> = {}): CloudAutomationRun => ({
	id: status, automationId: "a", scheduledFor: "2026-11-16T09:00:00Z", trigger: "schedule", status: status as CloudAutomationRun["status"], attemptCount: 1, createdAt: "2026-11-16T09:00:00Z", ...overrides,
});
const automation = (overrides: Partial<CloudAutomation> = {}): CloudAutomation => ({
	id: "a", projectId: "p", displayName: "Nightly debt", prompt: "List debt.", harness: "codex", mode: "trusted",
	rrule: "FREQ=HOURLY", timezone: "UTC", enabled: true, maxRuntimeMinutes: 60, deleteSessionOnComplete: false, branch: "ao/automation-a",
	runAs: { userId: "4fa6d173-cp" }, consecutiveFailures: 0, subscribed: true, canEdit: true,
	createdAt: "2026-11-16T00:00:00Z", updatedAt: "2026-11-16T00:00:00Z", ...overrides,
});
const page = <T>(items: T[]) => ({ items, page: { hasMore: false } });

function setup() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client: queryClient }, children);
	return { queryClient, wrapper };
}

beforeEach(() => {
	for (const fn of Object.values(mocks.client)) fn.mockReset();
	mocks.client.me.mockResolvedValue({ user: { id: "4fa6d173-cp" } });
	mocks.client.listAutomations.mockResolvedValue(page([]));
	mocks.client.listAutomationRuns.mockResolvedValue(page([]));
});

// Saving checks what a run needs (the control plane's preflight): each
// refusal reads as what to fix; a plan limit reads as one message whatever
// its code, and a code this app does not know keeps the server's words.
describe("cloudAutomationErrorMessage", () => {
	const t = ((key: string) => key) as unknown as Parameters<typeof cloudAutomationErrorMessage>[1];
	const refusal = (code: string) => new CloudCpError(`server says ${code}`, { status: 422, code });
	it.each(["repository_not_accessible", "repository_unreachable", "SANDBOX_QUOTA_EXCEEDED", "PLAN_REQUIRED"])("names %s", (code) => {
		expect(cloudAutomationErrorMessage(refusal(code), t)).toBe(`automations.cloud.error.${code}`);
	});
	it("reads every plan limit as one message, and keeps an unknown code's words", () => {
		expect(cloudAutomationErrorMessage(refusal("WORKER_SLOTS_FULL"), t)).toBe("automations.cloud.error.PLAN_LIMIT");
		expect(cloudAutomationErrorMessage(refusal("WEEKLY_LIMIT_REACHED"), t)).toBe("automations.cloud.error.PLAN_LIMIT");
		expect(cloudAutomationErrorMessage(refusal("SOMETHING_NEW"), t)).toBe("server says SOMETHING_NEW");
	});
	// A save is refused for a full workspace or plan only when its limit is
	// zero, which a bigger plan or limit fixes; Run now is refused for being
	// full right now, which freeing a session or waiting fixes.
	it("reads a full workspace or plan as a zero limit when saving, and as full right now otherwise", () => {
		expect(cloudAutomationErrorMessage(refusal("SANDBOX_QUOTA_EXCEEDED"), t, { saving: true })).toBe("automations.cloud.error.save.SANDBOX_QUOTA_EXCEEDED");
		expect(cloudAutomationErrorMessage(refusal("WORKER_SLOTS_FULL"), t, { saving: true })).toBe("automations.cloud.error.save.PLAN_LIMIT");
		expect(cloudAutomationErrorMessage(refusal("SESSION_LIMIT_REACHED"), t, { saving: true })).toBe("automations.cloud.error.save.PLAN_LIMIT");
		expect(cloudAutomationErrorMessage(refusal("SANDBOX_QUOTA_EXCEEDED"), t)).toBe("automations.cloud.error.SANDBOX_QUOTA_EXCEEDED");
		expect(cloudAutomationErrorMessage(refusal("WORKER_SLOTS_FULL"), t)).toBe("automations.cloud.error.PLAN_LIMIT");
		// Other refusals read the same either way.
		expect(cloudAutomationErrorMessage(refusal("PLAN_REQUIRED"), t, { saving: true })).toBe("automations.cloud.error.PLAN_REQUIRED");
	});
	it("names the agent a missing login is for when the request's harness is known", () => {
		const withArgs = ((key: string, options?: Record<string, unknown>) => options ? `${key} ${JSON.stringify(options)}` : key) as unknown as Parameters<typeof cloudAutomationErrorMessage>[1];
		expect(cloudAutomationErrorMessage(refusal("agent_provider_required"), withArgs, { harness: "codex" })).toBe('automations.cloud.error.agentLoginRequired {"agent":"Codex"}');
		expect(cloudAutomationErrorMessage(refusal("agent_provider_required"), withArgs)).toBe("automations.cloud.error.agent_provider_required");
		// Another refusal reads the same with or without the harness.
		expect(cloudAutomationErrorMessage(refusal("PLAN_REQUIRED"), withArgs, { harness: "codex" })).toBe("automations.cloud.error.PLAN_REQUIRED");
	});
});

// A run's error code reads in the person's language for every code the
// contract names (AutomationRun.errorCode); an unknown one keeps the server's words.
describe("cloudRunErrorMessage", () => {
	const t = ((key: string) => key) as unknown as Parameters<typeof cloudRunErrorMessage>[1];
	it.each(["STOPPED", "forbidden", "PLAN_REQUIRED", "SESSION_ENDED", "agent_provider_required"])("names %s", (errorCode) => {
		expect(cloudRunErrorMessage({ errorCode, errorMessage: "server words" }, t)).toBe(`automations.cloud.runError.${errorCode}`);
	});
	it("keeps the server's words for a code it does not know", () => {
		expect(cloudRunErrorMessage({ errorCode: "SOMETHING_NEW", errorMessage: "server words" }, t)).toBe("server words");
	});
});

describe("cloud automation polling", () => {
	it("follows a run in flight closely and idles slowly otherwise", () => {
		expect(cloudAutomationPollInterval(undefined)).toBe(60_000);
		expect(cloudAutomationPollInterval([])).toBe(60_000);
		expect(cloudAutomationPollInterval([{ latestRun: run("completed") }, { latestRun: undefined }])).toBe(60_000);
		for (const status of ["pending", "starting", "running"]) {
			expect(cloudAutomationPollInterval([{ latestRun: run("failed") }, { latestRun: run(status) }])).toBe(15_000);
		}
	});

	it("follows a run in flight closely when a newer skipped occurrence is the latest run", () => {
		const skipped = run("skipped", { errorCode: "PREVIOUS_RUN_ACTIVE" });
		expect(cloudAutomationPollInterval([{ latestRun: skipped, hiddenInFlightRun: run("running", { trigger: "manual" }) }])).toBe(15_000);
		expect(cloudAutomationPollInterval([{ latestRun: skipped }])).toBe(60_000);
	});

	it("polls a run history the same way", () => {
		expect(cloudRunsPollInterval(undefined)).toBe(60_000);
		expect(cloudRunsPollInterval([run("skipped"), run("timed_out")])).toBe(60_000);
		expect(cloudRunsPollInterval([run("running"), run("completed")])).toBe(15_000);
	});
});

describe("useCloudAutomations", () => {
	it("knows the signed-in person by their control-plane user id, never their sign-in account's", async () => {
		const { wrapper } = setup();
		const { result } = renderHook(() => useCloudAutomations(), { wrapper });
		await waitFor(() => expect(result.current.userId).toBe("4fa6d173-cp"));
	});

	it("does not guess who is signed in before the control plane says", () => {
		mocks.client.me.mockReturnValue(new Promise(() => undefined));
		const { wrapper } = setup();
		const { result } = renderHook(() => useCloudAutomations(), { wrapper });
		expect(result.current.userId).toBeUndefined();
	});

	it("finds the run in flight that a newer skipped occurrence hides, and follows it closely", async () => {
		const now = Date.now();
		const skipped = run("skipped", { id: "skip", scheduledFor: iso(now - 60_000), finishedAt: iso(now - 50_000), errorCode: "PREVIOUS_RUN_ACTIVE" });
		const manual = run("running", { id: "manual", trigger: "manual", scheduledFor: iso(now - 5 * 60_000) });
		mocks.client.listAutomations.mockResolvedValue(page([automation({ latestRun: skipped })]));
		mocks.client.listAutomationRuns.mockResolvedValue(page([skipped, manual]));
		const { wrapper } = setup();
		const { result } = renderHook(() => useCloudAutomations(), { wrapper });
		await waitFor(() => expect(result.current.data).toHaveLength(1));
		expect(mocks.client.listAutomationRuns).toHaveBeenCalledWith("org-1", "a", expect.objectContaining({ limit: expect.any(Number) }), expect.anything());
		expect(cloudAutomationPollInterval(result.current.data)).toBe(15_000);
		expect(cloudAutomationActiveRun(result.current.data![0])?.id).toBe("manual");
	});

	it("reports no run in flight when the run behind the skip has settled", async () => {
		const now = Date.now();
		const skipped = run("skipped", { id: "skip", scheduledFor: iso(now - 60_000), finishedAt: iso(now - 50_000), errorCode: "PREVIOUS_RUN_ACTIVE" });
		mocks.client.listAutomations.mockResolvedValue(page([automation({ latestRun: skipped })]));
		// Only one run goes at a time: one older than a settled run cannot still be going.
		mocks.client.listAutomationRuns.mockResolvedValue(page([skipped, run("completed", { id: "done", trigger: "manual", scheduledFor: iso(now - 5 * 60_000) }), run("running", { id: "older" })]));
		const { wrapper } = setup();
		const { result } = renderHook(() => useCloudAutomations(), { wrapper });
		await waitFor(() => expect(result.current.data).toHaveLength(1));
		expect(mocks.client.listAutomationRuns).toHaveBeenCalled();
		expect(cloudAutomationActiveRun(result.current.data![0])).toBeUndefined();
		expect(cloudAutomationPollInterval(result.current.data)).toBe(60_000);
	});

	it("looks behind a skip only while a run that started before it could still be going", async () => {
		const now = Date.now();
		// A run stops at its automation's time limit, so a skip older than that hides nothing.
		const old = run("skipped", { id: "old", scheduledFor: iso(now - 2 * 3_600_000), finishedAt: iso(now - 2 * 3_600_000), errorCode: "PREVIOUS_RUN_ACTIVE" });
		mocks.client.listAutomations.mockResolvedValue(page([
			automation({ id: "a", latestRun: old }),
			automation({ id: "b", latestRun: run("completed", { automationId: "b", finishedAt: iso(now) }) }),
			automation({ id: "c", latestRun: run("running", { automationId: "c" }) }),
		]));
		const { wrapper } = setup();
		const { result } = renderHook(() => useCloudAutomations(), { wrapper });
		await waitFor(() => expect(result.current.data).toHaveLength(3));
		expect(mocks.client.listAutomationRuns).not.toHaveBeenCalled();
	});
});

describe("useRunCloudAutomationNow", () => {
	it("shows the run it asked for as in flight at once, so Run now stays off", async () => {
		const { queryClient, wrapper } = setup();
		queryClient.setQueryData(listKey, [automation({ latestRun: run("completed") })]);
		const queued = run("pending", { id: "queued", trigger: "manual" });
		mocks.client.runAutomationNow.mockResolvedValue({ run: queued });
		// The refreshed list is slow to answer.
		mocks.client.listAutomations.mockReturnValue(new Promise(() => undefined));
		const { result } = renderHook(() => ({ list: useCloudAutomations(), runNow: useRunCloudAutomationNow() }), { wrapper });
		await act(async () => { await result.current.runNow.mutateAsync("a"); });
		const cached = queryClient.getQueryData<CloudAutomation[]>(listKey);
		expect(cached?.[0].latestRun?.id).toBe("queued");
		expect(cloudAutomationPollInterval(cached)).toBe(15_000);
	});

	it("refreshes the list when the control plane says a run is already going", async () => {
		const { queryClient, wrapper } = setup();
		queryClient.setQueryData(listKey, [automation({ latestRun: run("completed") })]);
		mocks.client.runAutomationNow.mockRejectedValue(new CloudCpError("A run is in flight.", { status: 409, code: "AUTOMATION_RUN_ACTIVE" }));
		const { result } = renderHook(() => ({ list: useCloudAutomations(), runNow: useRunCloudAutomationNow() }), { wrapper });
		await waitFor(() => expect(mocks.client.listAutomations).toHaveBeenCalledTimes(1));
		await act(async () => { await result.current.runNow.mutateAsync("a").catch(() => undefined); });
		await waitFor(() => expect(mocks.client.listAutomations).toHaveBeenCalledTimes(2));
	});
});
