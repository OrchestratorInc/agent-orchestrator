import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { cloudAutomationPollInterval, cloudRunsPollInterval, useCloudAutomations, type CloudAutomationRun } from "./useCloudAutomations";

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

const run = (status: string, overrides: Partial<CloudAutomationRun> = {}): CloudAutomationRun => ({
	id: status, automationId: "a", scheduledFor: "2026-11-16T09:00:00Z", trigger: "schedule", status: status as CloudAutomationRun["status"], attemptCount: 1, createdAt: "2026-11-16T09:00:00Z", ...overrides,
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

describe("cloud automation polling", () => {
	it("follows a run in flight closely and idles slowly otherwise", () => {
		expect(cloudAutomationPollInterval(undefined)).toBe(60_000);
		expect(cloudAutomationPollInterval([])).toBe(60_000);
		expect(cloudAutomationPollInterval([{ latestRun: run("completed") }, { latestRun: undefined }])).toBe(60_000);
		for (const status of ["pending", "starting", "running"]) {
			expect(cloudAutomationPollInterval([{ latestRun: run("failed") }, { latestRun: run(status) }])).toBe(15_000);
		}
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
});
