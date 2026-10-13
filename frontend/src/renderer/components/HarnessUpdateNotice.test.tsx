import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { HarnessUpdateNotice } from "./HarnessUpdateNotice";
import { apiClient } from "../lib/api-client";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { appI18n } from "../i18n";
import { useHarnessActionRequest, updateAdvisoryQueryKey, installJobsQueryKey, installerQueryKey } from "../hooks/useHarnessUpdates";
import { useUiStore } from "../stores/ui-store";
import type { AgentReadiness } from "../hooks/useAgentReadinessQuery";

function mountNotice(enabled = true, client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })) {
	const view = render(<QueryClientProvider client={client}><HarnessUpdateNotice enabled={enabled} /></QueryClientProvider>);
	return { client, ...view };
}

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}

function mockStack() {
	const readiness = { agents: [
		agentReadiness("claude-code", "Claude Code", { usageCount: 5 }),
		agentReadiness("codex", "Codex", { usageCount: 30, lastUsedAt: "2026-10-06T12:00:00Z" }),
		agentReadiness("cursor", "Cursor", { usageCount: 30, lastUsedAt: "2026-10-07T12:00:00Z" }),
	] };
	const original = vi.mocked(apiClient.GET).getMockImplementation()!;
	vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
		if (path === "/api/v1/agents/readiness") return { data: readiness } as never;
		if (path === "/api/v1/agents/{agent}/update-advisory") return { data: { status: "behind_latest", currentVersion: "1.0.0", latestVersion: "2.0.0" } } as never;
		return (original as (path: string, options: unknown) => Promise<never>)(path, options);
	});
	vi.mocked(apiClient.POST).mockResolvedValue({ data: readiness } as never);
}

function mockUpdatableStack() {
	mockStack();
	const original = vi.mocked(apiClient.GET).getMockImplementation()!;
	vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
		if (path === "/api/v1/agents/installers") return { data: { agents: ["claude-code", "codex", "cursor"].map((agentId) => ({
			agentId, methods: [{ id: "official-installer", available: true, updateAvailable: true }],
		})) } } as never;
		if (path === "/api/v1/agents/auth-plans") return { data: { plans: ["claude-code", "codex", "cursor"].map((agentId) => ({ agentId, action: "login", available: true })) } } as never;
		if (path === "/api/v1/agents/{agent}/update-advisory") return { data: {
			status: "behind_latest", currentVersion: "1.0.0", latestVersion: "2.0.0", maintenanceMethod: "official-installer",
		} } as never;
		return (original as (path: string, options: unknown) => Promise<never>)(path, options);
	});
}

describe("startup harness update notice", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		localStorage.clear();
		useHarnessActionRequest.getState().setRequest(null);
		const readiness = { agents: [agentReadiness("claude-code", "Claude Code")] };
		vi.spyOn(apiClient, "GET").mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: readiness } as never;
			if (path === "/api/v1/agents/installers") return { data: { agents: [{ agentId: "claude-code", documentationUrl: "https://code.claude.com/docs/en/setup", methods: [{ id: "official-installer", available: true, updateAvailable: true }] }] } } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "claude-code", action: "login", available: true }] } } as never;
			if (path === "/api/v1/agents/{agent}/update-advisory") return { data: { agentId: "claude-code", status: "behind_latest", currentVersion: "2.1.291", latestVersion: "2.1.292", maintenanceMethod: "official-installer", source: "official-release", checkedAt: "2026-10-07T00:00:00Z" } } as never;
			return { data: undefined } as never;
		});
		vi.spyOn(apiClient, "POST").mockResolvedValue({ data: readiness } as never);
	});
	afterEach(() => { cleanup(); vi.restoreAllMocks(); });

	it("finds updates without mounting Harness settings and starts no installation automatically", async () => {
		mountNotice();
		const notice = await screen.findByRole("region", { name: "Harness updates available" });
		expect(within(notice).getByText("v2.1.291")).toBeInTheDocument();
		expect(within(notice).getByText("v2.1.292")).toBeInTheDocument();
		expect(within(notice).getByRole("button", { name: "Update" })).toBeEnabled();
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("updates in place from the notice without opening Settings", async () => {
		const before = useUiStore.getState().settingsModal;
		mountNotice();
		const notice = await screen.findByRole("region", { name: "Harness updates available" });
		await userEvent.click(within(notice).getByRole("button", { name: "Update" }));
		// The click re-checks the release, then installs exactly that one here.
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "claude-code" } },
			body: { method: "official-installer", operation: "update", expectedVersion: "2.1.292" },
		}));
		expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/{agent}/update-advisory", expect.objectContaining({ params: expect.objectContaining({ query: { refresh: true } }) }));
		expect(useHarnessActionRequest.getState().request).toBeNull();
		expect(useUiStore.getState().settingsModal).toBe(before);
	});

	it("updates the collapsed notification stack once and continues after a failed start", async () => {
		mockUpdatableStack();
		const pending = deferred<never>();
		const original = vi.mocked(apiClient.POST).getMockImplementation()!;
		const started: string[] = [];
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => {
			if (path !== "/api/v1/agents/{agent}/install") return (original as (path: string, options: unknown) => Promise<never>)(path, options);
			const agentId = (options as { params: { path: { agent: string } } }).params.path.agent;
			started.push(agentId);
			if (agentId === "cursor") return pending.promise;
			return { data: { target: agentId, status: "installing" } } as never;
		});
		const before = useUiStore.getState().settingsModal;
		mountNotice();
		const button = await screen.findByRole("button", { name: "Update all (3)" });
		expect(screen.getAllByRole("article")).toHaveLength(1);
		await userEvent.dblClick(button);
		await waitFor(() => expect(started).toEqual(["cursor"]));
		expect(screen.getByRole("button", { name: "Starting updates…" })).toBeDisabled();
		expect(within(screen.getByRole("article", { name: "Codex" })).getByRole("button", { name: "Update" })).toBeDisabled();
		await act(async () => pending.resolve({ error: { message: "Cursor update refused" } } as never));
		await waitFor(() => expect(started).toEqual(["cursor", "codex", "claude-code"]));
		expect(await screen.findByRole("alert")).toHaveTextContent("Cursor update refused");
		expect(useUiStore.getState().settingsModal).toBe(before);
		expect(useHarnessActionRequest.getState().request).toBeNull();
	});

	it("excludes busy, login-required and unsupported harnesses from notification Update all", async () => {
		mockUpdatableStack();
		const { client } = mountNotice();
		await screen.findByRole("button", { name: "Update all (3)" });
		act(() => {
			client.setQueryData(["agent-readiness"], { agents: [
				agentReadiness("claude-code", "Claude Code"),
				agentReadiness("codex", "Codex", { authentication: "unauthorized" }),
				agentReadiness("cursor", "Cursor"),
			] });
			client.setQueryData(installJobsQueryKey, [{ target: "cursor", status: "installing" }]);
		});
		expect(await screen.findByRole("button", { name: "Update all (1)" })).toBeEnabled();
		act(() => client.setQueryData(installerQueryKey, []));
		expect(await screen.findByRole("button", { name: "Update all" })).toBeDisabled();
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("explains a refused update in the notice", async () => {
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path) => path === "/api/v1/agents/{agent}/install"
			? { error: { code: "HARNESS_ACTIVE", message: "End claude-code session s-1 before changing its installation" } } as never
			: { data: { agents: [agentReadiness("claude-code", "Claude Code")] } } as never);
		vi.mocked(apiClient.GET).mockImplementation(get);
		mountNotice();
		const notice = await screen.findByRole("region", { name: "Harness updates available" });
		await userEvent.click(within(notice).getByRole("button", { name: "Update" }));
		expect(await within(notice).findByRole("alert")).toHaveTextContent("End claude-code session s-1");
		expect(within(notice).getByRole("button", { name: "Retry update" })).toBeEnabled();
	});

	it("waits for the daemon before starting background requests", () => {
		mountNotice(false);
		expect(apiClient.GET).not.toHaveBeenCalled();
		expect(apiClient.POST).not.toHaveBeenCalled();
	});

	it("waits for ensure completion even with cached installation and update evidence", async () => {
		const pending = deferred<{ data: AgentReadiness }>();
		vi.mocked(apiClient.POST).mockReturnValueOnce(pending.promise as never);
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const readiness = { agents: [agentReadiness("claude-code", "Claude Code")] };
		client.setQueryData(["agent-readiness"], readiness);
		client.setQueryData(updateAdvisoryQueryKey("claude-code"), { status: "behind_latest", currentVersion: "2.1.291", latestVersion: "2.1.292" });
		mountNotice(true, client);
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalled());
		expect(screen.queryByRole("region")).toBeNull();
		expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path]) => path === "/api/v1/agents/{agent}/update-advisory")).toBe(false);
		await act(async () => pending.resolve({ data: readiness }));
		expect(await screen.findByRole("article", { name: "Claude Code" })).toBeInTheDocument();
	});

	it("does not open the gate when ensure fails", async () => {
		vi.mocked(apiClient.POST).mockRejectedValue(new Error("readiness unavailable"));
		const { client } = mountNotice();
		await waitFor(() => expect(client.getQueryData(["agent-readiness"])).toBeDefined());
		expect(screen.queryByRole("region")).toBeNull();
		expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path]) => path === "/api/v1/agents/{agent}/update-advisory")).toBe(false);
	});

	it.each(["unknown", "configured"] as const)("does not mistake %s authentication for a required login", async (authentication) => {
		const { client } = mountNotice();
		await screen.findByRole("region");
		act(() => client.setQueryData(["agent-readiness"], { agents: [agentReadiness("claude-code", "Claude Code", { authentication })] }));
		expect(screen.queryByRole("button", { name: "Login" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Update" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything()));
		expect(useHarnessActionRequest.getState().request).toBeNull();
	});

	it("ranks by usage then recency, and closing only the top release reveals the next", async () => {
		mockStack();
		mountNotice();
		expect(await screen.findByRole("article", { name: "Cursor" })).toBeInTheDocument();
		expect(screen.getAllByRole("article")).toHaveLength(1);
		await userEvent.click(screen.getByRole("button", { name: "Dismiss Cursor update" }));
		expect(screen.getByRole("article", { name: "Codex" })).toBeInTheDocument();
		expect(JSON.parse(localStorage.getItem("ao.harness-update-dismissals.v1")!)).toEqual([JSON.stringify(["local", "cursor", "2.0.0"])]);
		await userEvent.click(screen.getByRole("button", { name: "Dismiss Codex update" }));
		expect(screen.getByRole("article", { name: "Claude Code" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Dismiss Claude Code update" }));
		expect(screen.queryByRole("region")).toBeNull();
	});

	it("expands and collapses the stack, with independent dismissal of expanded cards", async () => {
		mockStack();
		mountNotice();
		const topCard = await screen.findByRole("article", { name: "Cursor" });
		expect(screen.queryByText("Harness updates available")).toBeNull();
		// The toggle sits above the stack, not inside the top notice.
		expect(within(topCard).queryByRole("button", { name: "Show all (3)" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Show all (3)" }));
		expect(screen.getAllByRole("article").map((card) => card.getAttribute("aria-label"))).toEqual(["Cursor", "Codex", "Claude Code"]);
		expect(screen.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");
		await userEvent.click(screen.getByRole("button", { name: "Dismiss Codex update" }));
		expect(screen.getAllByRole("article")).toHaveLength(2);
		await userEvent.click(screen.getByRole("button", { name: "Show less" }));
		expect(screen.getAllByRole("article")).toHaveLength(1);
		expect(screen.getByRole("article", { name: "Cursor" })).toBeInTheDocument();
	});

	it("waits for slower advisories before choosing the top harness", async () => {
		mockStack();
		const pending = deferred<never>();
		const original = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation((path, options) => {
			const agentId = (options as { params?: { path?: { agent?: string } } } | undefined)?.params?.path?.agent;
			if (path === "/api/v1/agents/{agent}/update-advisory" && agentId === "cursor") return pending.promise;
			return (original as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		const { client } = mountNotice();
		await waitFor(() => expect(client.getQueryData(updateAdvisoryQueryKey("codex"))).toBeDefined());
		expect(screen.queryByRole("region")).toBeNull();
		await act(async () => pending.resolve({ data: { status: "behind_latest", currentVersion: "1.0.0", latestVersion: "2.0.0" } } as never));
		expect(await screen.findByRole("article", { name: "Cursor" })).toBeInTheDocument();
	});

	it("keeps other update notices when one harness lookup fails", async () => {
		mockStack();
		const original = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation((path, options) => {
			const agentId = (options as { params?: { path?: { agent?: string } } } | undefined)?.params?.path?.agent;
			if (path === "/api/v1/agents/{agent}/update-advisory" && agentId === "cursor") return Promise.reject(new Error("lookup timed out"));
			return (original as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		mountNotice();
		expect(await screen.findByRole("article", { name: "Codex" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show all (2)" })).toBeInTheDocument();
	});

	it("preserves only the individually dismissed releases on remount", async () => {
		mockStack();
		const view = mountNotice();
		await userEvent.click(await screen.findByRole("button", { name: "Dismiss Cursor update" }));
		view.unmount();
		mountNotice();
		expect(await screen.findByRole("article", { name: "Codex" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Show all (2)" }));
		expect(screen.getAllByRole("article").map((card) => card.getAttribute("aria-label"))).toEqual(["Codex", "Claude Code"]);
	});

	it("offers login first when both login and update are needed", async () => {
		const { client } = mountNotice();
		await screen.findByRole("region", { name: "Harness updates available" });
		act(() => client.setQueryData(["agent-readiness"], { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" })] }));
		await userEvent.click(await screen.findByRole("button", { name: "Login" }));
		expect(useHarnessActionRequest.getState().request?.action).toBe("login");
		expect(screen.queryByRole("button", { name: "Update" })).toBeNull();
	});

	it("dismisses the shown release across remounts but shows a newer target", async () => {
		const view = mountNotice();
		await screen.findByRole("region", { name: "Harness updates available" });
		await userEvent.click(screen.getByRole("button", { name: "Dismiss Claude Code update" }));
		expect(screen.queryByRole("region")).toBeNull();
		view.unmount();
		const { client } = mountNotice();
		await waitFor(() => expect(client.getQueryData(updateAdvisoryQueryKey("claude-code"))).toBeDefined());
		expect(screen.queryByRole("region")).toBeNull();
		act(() => client.setQueryData(updateAdvisoryQueryKey("claude-code"), { agentId: "claude-code", status: "behind_latest", currentVersion: "2.1.291", latestVersion: "2.1.293" }));
		expect(await screen.findByText("v2.1.293")).toBeInTheDocument();
	});

	it.each(["unknown", "current"])("does not notify for %s results", async (status) => {
		const { client } = mountNotice();
		await screen.findByRole("region");
		act(() => client.setQueryData(updateAdvisoryQueryKey("claude-code"), { agentId: "claude-code", status, currentVersion: "2.1.291", latestVersion: "2.1.292" }));
		await waitFor(() => expect(screen.queryByRole("region")).toBeNull());
	});
});
