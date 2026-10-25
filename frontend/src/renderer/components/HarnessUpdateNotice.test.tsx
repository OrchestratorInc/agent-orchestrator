import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { HarnessUpdatesProvider, HarnessUpdatesRailButton, HarnessUpdatesRow } from "./HarnessUpdateNotice";
import { apiClient } from "../lib/api-client";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { appI18n } from "../i18n";
import { updateAdvisoryQueryKey, installJobsQueryKey, installerQueryKey } from "../hooks/useHarnessUpdates";
import { TooltipProvider } from "./ui/tooltip";
import { useUiStore } from "../stores/ui-store";
import type { AgentReadiness } from "../hooks/useAgentReadinessQuery";

const TRIGGER = /agent updates? available/;

function mountMenu(enabled = true, client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } }), rail = false) {
	const view = render(<QueryClientProvider client={client}><TooltipProvider><HarnessUpdatesProvider enabled={enabled}>
		{rail ? <HarnessUpdatesRailButton /> : <HarnessUpdatesRow />}
	</HarnessUpdatesProvider></TooltipProvider></QueryClientProvider>);
	return { client, ...view };
}

const trigger = () => screen.queryByRole("button", { name: TRIGGER });

/** Opens the updates popover from the sidebar row (if it is not open already). */
async function openMenu() {
	const open = screen.queryByRole("dialog", { name: "Agent updates" });
	if (open) return open;
	await userEvent.click(await screen.findByRole("button", { name: TRIGGER }));
	return screen.findByRole("dialog", { name: "Agent updates" });
}

const rowNames = (menu: HTMLElement) => within(menu).getAllByRole("listitem").map((row) => row.getAttribute("aria-label"));

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
		if (path === "/api/v1/agents/installers") return { data: { agents: readiness.agents.map(({ id: agentId }) => ({ agentId, methods: [{ id: "official-installer", available: true, updateAvailable: true }] })) } } as never;
		if (path === "/api/v1/agents/{agent}/update-advisory") return { data: { status: "behind_latest", currentVersion: "1.0.0", latestVersion: "2.0.0", maintenanceMethod: "official-installer" } } as never;
		return (original as (path: string, options: unknown) => Promise<never>)(path, options);
	});
	vi.mocked(apiClient.POST).mockResolvedValue({ data: readiness } as never);
}

function mockUpdatableStack() {
	mockStack();
	const original = vi.mocked(apiClient.GET).getMockImplementation()!;
	vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
		if (path === "/api/v1/agents/auth-plans") return { data: { plans: ["claude-code", "codex", "cursor"].map((agentId) => ({ agentId, action: "login", available: true })) } } as never;
		return (original as (path: string, options: unknown) => Promise<never>)(path, options);
	});
}

describe("sidebar agent updates", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		localStorage.clear();
		useUiStore.setState({ settingsModal: null });
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

	it("shows one quiet footer row with the count, and nothing floats over the app", async () => {
		mockStack();
		mountMenu();
		const row = await screen.findByRole("button", { name: "3 agent updates available" });
		expect(row).toHaveTextContent("Agent updates3");
		expect(row).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByRole("region")).toBeNull();
		expect(screen.queryByRole("dialog")).toBeNull();
	});

	it("opens one shared list anchored to the row", async () => {
		mockStack();
		mountMenu();
		const menu = await openMenu();
		expect(screen.getByRole("button", { name: TRIGGER })).toHaveAttribute("aria-expanded", "true");
		expect(rowNames(menu)).toEqual(["Cursor", "Codex", "Claude Code"]);
		expect(within(menu).getAllByText("v1.0.0")).toHaveLength(3);
		expect(within(menu).getAllByRole("button", { name: "Update" })).toHaveLength(3);
		expect(within(menu).getByRole("button", { name: "Update all" })).toBeEnabled();
	});

	it("shows each row's Update as a quiet secondary button at rest, below the filled Update all", async () => {
		mockStack();
		mountMenu();
		const menu = await openMenu();
		for (const update of within(menu).getAllByRole("button", { name: "Update" })) {
			expect(update).toHaveClass("bg-secondary");
			expect(update).not.toHaveClass("opacity-0");
		}
		expect(within(menu).getByRole("button", { name: "Update all" })).not.toHaveClass("bg-secondary");
	});

	it("opens the same list from the collapsed icon rail", async () => {
		mockStack();
		mountMenu(true, undefined, true);
		await userEvent.click(await screen.findByRole("button", { name: "3 agent updates available" }));
		const menu = await screen.findByRole("dialog", { name: "Agent updates" });
		expect(rowNames(menu)).toEqual(["Cursor", "Codex", "Claude Code"]);
	});

	it("closes when its sidebar row leaves keyboard navigation", async () => {
		const view = mountMenu();
		await openMenu();
		view.rerender(<QueryClientProvider client={view.client}><TooltipProvider><HarnessUpdatesProvider enabled>
			<HarnessUpdatesRow tabIndex={-1} />
		</HarnessUpdatesProvider></TooltipProvider></QueryClientProvider>);
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Agent updates" })).toBeNull());
		expect(trigger()).toHaveAttribute("tabindex", "-1");
	});

	it("preserves a failed update after closing and reopening the popover", async () => {
		vi.mocked(apiClient.POST).mockImplementation(async (path) => path === "/api/v1/agents/{agent}/install"
			? { error: { message: "Update refused" } } as never
			: { data: { agents: [agentReadiness("claude-code", "Claude Code")] } } as never);
		mountMenu();
		await userEvent.click(within(await openMenu()).getByRole("button", { name: "Update" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Update refused");
		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Agent updates" })).toBeNull());
		expect(trigger()).toHaveFocus();
		const menu = await openMenu();
		expect(within(menu).getByRole("alert")).toHaveTextContent("Update refused");
		expect(within(menu).getByRole("button", { name: "Retry update" })).toBeEnabled();
	});

	it("hides the row when no agent has an update", async () => {
		const { client } = mountMenu();
		await screen.findByRole("button", { name: "1 agent update available" });
		act(() => client.setQueryData(updateAdvisoryQueryKey("claude-code"), { agentId: "claude-code", status: "current", currentVersion: "2.1.292", latestVersion: "2.1.292" }));
		await waitFor(() => expect(trigger()).toBeNull());
	});

	it("finds updates without mounting Harness settings and starts no installation automatically", async () => {
		mountMenu();
		const menu = await openMenu();
		expect(within(menu).getByText("v2.1.291")).toBeInTheDocument();
		expect(within(menu).getByText("v2.1.292")).toBeInTheDocument();
		expect(within(menu).getByRole("button", { name: "Update" })).toBeEnabled();
		// A single update needs no Update all.
		expect(within(menu).queryByRole("button", { name: "Update all" })).toBeNull();
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("updates in place from the list without opening Settings", async () => {
		const before = useUiStore.getState().settingsModal;
		mountMenu();
		const menu = await openMenu();
		await userEvent.click(within(menu).getByRole("button", { name: "Update" }));
		// The click re-checks the release, then installs exactly that one here.
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "claude-code" } },
			body: { method: "official-installer", operation: "update", expectedVersion: "2.1.292" },
		}));
		expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/{agent}/update-advisory", expect.objectContaining({ params: expect.objectContaining({ query: { refresh: true } }) }));
		expect(useUiStore.getState().settingsModal).toBe(before);
	});

	it("runs Update all once and continues after a failed start", async () => {
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
		mountMenu();
		const menu = await openMenu();
		await userEvent.dblClick(within(menu).getByRole("button", { name: "Update all" }));
		await waitFor(() => expect(started).toEqual(["cursor"]));
		expect(within(menu).getByRole("button", { name: "Starting updates…" })).toBeDisabled();
		expect(within(within(menu).getByRole("listitem", { name: "Codex" })).getByRole("button", { name: "Update" })).toBeDisabled();
		await act(async () => pending.resolve({ error: { message: "Cursor update refused" } } as never));
		await waitFor(() => expect(started).toEqual(["cursor", "codex", "claude-code"]));
		expect(await within(menu).findByRole("alert")).toHaveTextContent("Cursor update refused");
		expect(useUiStore.getState().settingsModal).toBe(before);
	});

	it("excludes busy, login-required and unsupported harnesses from Update all", async () => {
		mockUpdatableStack();
		const { client } = mountMenu();
		const menu = await openMenu();
		expect(within(menu).getByRole("button", { name: "Update all" })).toBeEnabled();
		act(() => {
			client.setQueryData(["agent-readiness"], { agents: [
				agentReadiness("claude-code", "Claude Code"),
				agentReadiness("codex", "Codex", { authentication: "unauthorized" }),
				agentReadiness("cursor", "Cursor"),
			] });
			client.setQueryData(installJobsQueryKey, [{ target: "cursor", status: "installing" }]);
		});
		// Claude Code is still eligible.
		expect(within(menu).getByRole("button", { name: "Update all" })).toBeEnabled();
		act(() => client.setQueryData(installerQueryKey, []));
		await waitFor(() => expect(trigger()).toBeNull());
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("explains a refused update in the list and offers a retry", async () => {
		vi.mocked(apiClient.POST).mockImplementation(async (path) => path === "/api/v1/agents/{agent}/install"
			? { error: { code: "INSTALL_ACTIVE", message: "An install job is already active for claude-code" } } as never
			: { data: { agents: [agentReadiness("claude-code", "Claude Code")] } } as never);
		mountMenu();
		const menu = await openMenu();
		await userEvent.click(within(menu).getByRole("button", { name: "Update" }));
		expect(await within(menu).findByRole("alert")).toHaveTextContent("An install job is already active");
		expect(within(menu).getByRole("button", { name: "Retry update" })).toBeEnabled();
		expect(within(menu).getByRole("button", { name: "Show diagnostics" })).toBeEnabled();
	});

	it("keeps a failed update explained after a refresh reports the harness current", async () => {
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		let replaced = false;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			// The updater replaced the binary before it exited with an error.
			if (path === "/api/v1/agents/{agent}/update-advisory" && replaced) return { data: { agentId: "claude-code", status: "current", currentVersion: "2.1.292", latestVersion: "2.1.292", maintenanceMethod: "official-installer" } } as never;
			return (get as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path !== "/api/v1/agents/{agent}/install") return { data: { agents: [agentReadiness("claude-code", "Claude Code")] } } as never;
			replaced = true;
			return { data: { target: "claude-code", status: "installing" } } as never;
		});
		const { client } = mountMenu();
		const menu = await openMenu();
		await userEvent.click(within(menu).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(client.getQueryData(installJobsQueryKey)).toEqual([expect.objectContaining({ status: "installing" })]));
		act(() => client.setQueryData(installJobsQueryKey, [{ target: "claude-code", status: "failed", error: "Post-install verification failed" }]));
		await waitFor(() => expect(client.getQueryData(updateAdvisoryQueryKey("claude-code"))).toMatchObject({ status: "current" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Post-install verification failed");
		await userEvent.click(screen.getByRole("button", { name: "Dismiss Claude Code update" }));
		await waitFor(() => expect(trigger()).toBeNull());
	});

	it("keeps a failed update on the newer release the click-time check chose", async () => {
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		let started = false;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/update-advisory") {
				if (started) return { data: { agentId: "claude-code", status: "current", currentVersion: "2.1.293", latestVersion: "2.1.293", maintenanceMethod: "official-installer" } } as never;
				// A release published after the row was checked.
				if ((options as { params?: { query?: { refresh?: boolean } } })?.params?.query?.refresh) return { data: { agentId: "claude-code", status: "behind_latest", currentVersion: "2.1.291", latestVersion: "2.1.293", maintenanceMethod: "official-installer" } } as never;
			}
			return (get as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path !== "/api/v1/agents/{agent}/install") return { data: { agents: [agentReadiness("claude-code", "Claude Code")] } } as never;
			started = true;
			return { data: { target: "claude-code", status: "installing" } } as never;
		});
		const { client } = mountMenu();
		const menu = await openMenu();
		expect(within(menu).getByText("v2.1.292")).toBeInTheDocument();
		await userEvent.click(within(menu).getByRole("button", { name: "Update" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "claude-code" } }, body: { method: "official-installer", operation: "update", expectedVersion: "2.1.293" } });
		await waitFor(() => expect(client.getQueryData(installJobsQueryKey)).toEqual([expect.objectContaining({ status: "installing" })]));
		act(() => client.setQueryData(installJobsQueryKey, [{ target: "claude-code", status: "failed", error: "Updater failed" }]));
		await waitFor(() => expect(client.getQueryData(updateAdvisoryQueryKey("claude-code"))).toMatchObject({ status: "current" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Updater failed");
		expect(screen.getByText("v2.1.293")).toBeInTheDocument();
		expect(screen.queryByText("v2.1.292")).toBeNull();
	});

	it("stays out of the way while Settings is open", async () => {
		mountMenu();
		await screen.findByRole("button", { name: TRIGGER });
		act(() => useUiStore.getState().openGlobalSettings("general"));
		expect(trigger()).toBeNull();
		act(() => useUiStore.getState().closeSettings());
		expect(await screen.findByRole("button", { name: TRIGGER })).toBeInTheDocument();
	});

	it("refreshes a harness whose update finished before the start request returned", async () => {
		const readiness = { agents: [agentReadiness("claude-code", "Claude Code")] };
		vi.mocked(apiClient.POST).mockImplementation(async (path) => path === "/api/v1/agents/{agent}/install"
			? { data: { target: "claude-code", status: "succeeded" } } as never
			: { data: readiness } as never);
		mountMenu();
		const menu = await openMenu();
		vi.mocked(apiClient.POST).mockClear();
		await userEvent.click(within(menu).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/readiness/ensure", { body: { agentIds: ["claude-code"], purpose: "display" } }));
	});

	it("reloads installer methods when the advisory confirms an owner they lacked", async () => {
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		let installerLoads = 0;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/installers") {
				installerLoads += 1;
				const methods = installerLoads === 1 ? [] : [{ id: "homebrew", available: true, updateAvailable: true }];
				return { data: { agents: [{ agentId: "claude-code", methods }] } } as never;
			}
			if (path === "/api/v1/agents/{agent}/update-advisory") return { data: { agentId: "claude-code", status: "behind_latest", currentVersion: "2.1.291", latestVersion: "2.1.292", maintenanceMethod: "homebrew", source: "homebrew", checkedAt: "2026-10-07T00:00:00Z" } } as never;
			return (get as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		mountMenu();
		const menu = await openMenu();
		expect(await within(menu).findByRole("button", { name: "Update" })).toBeEnabled();
		expect(installerLoads).toBeGreaterThan(1);
	});

	it("waits for the daemon before starting background requests", () => {
		mountMenu(false);
		expect(apiClient.GET).not.toHaveBeenCalled();
		expect(apiClient.POST).not.toHaveBeenCalled();
		expect(trigger()).toBeNull();
	});

	it("waits for ensure completion even with cached installation and update evidence", async () => {
		const pending = deferred<{ data: AgentReadiness }>();
		vi.mocked(apiClient.POST).mockReturnValueOnce(pending.promise as never);
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const readiness = { agents: [agentReadiness("claude-code", "Claude Code")] };
		client.setQueryData(["agent-readiness"], readiness);
		client.setQueryData(updateAdvisoryQueryKey("claude-code"), { status: "behind_latest", currentVersion: "2.1.291", latestVersion: "2.1.292", maintenanceMethod: "official-installer" });
		mountMenu(true, client);
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalled());
		expect(trigger()).toBeNull();
		expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path]) => path === "/api/v1/agents/{agent}/update-advisory")).toBe(false);
		await act(async () => pending.resolve({ data: readiness }));
		const menu = await openMenu();
		expect(within(menu).getByRole("listitem", { name: "Claude Code" })).toBeInTheDocument();
	});

	it("does not open the gate when ensure fails", async () => {
		vi.mocked(apiClient.POST).mockRejectedValue(new Error("readiness unavailable"));
		const { client } = mountMenu();
		await waitFor(() => expect(client.getQueryData(["agent-readiness"])).toBeDefined());
		expect(trigger()).toBeNull();
		expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path]) => path === "/api/v1/agents/{agent}/update-advisory")).toBe(false);
	});

	it.each(["unknown", "configured"] as const)("does not mistake %s authentication for a required login", async (authentication) => {
		const { client } = mountMenu();
		const menu = await openMenu();
		act(() => client.setQueryData(["agent-readiness"], { agents: [agentReadiness("claude-code", "Claude Code", { authentication })] }));
		expect(within(menu).queryByRole("button", { name: "Login" })).toBeNull();
		await userEvent.click(within(menu).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything()));
	});

	it("ranks by usage then recency, and dismissing one release keeps the others", async () => {
		mockStack();
		mountMenu();
		const menu = await openMenu();
		expect(rowNames(menu)).toEqual(["Cursor", "Codex", "Claude Code"]);
		await userEvent.click(within(menu).getByRole("button", { name: "Dismiss Cursor update" }));
		expect(rowNames(menu)).toEqual(["Codex", "Claude Code"]);
		expect(screen.getByRole("button", { name: "2 agent updates available" })).toBeInTheDocument();
		expect(JSON.parse(localStorage.getItem("ao.harness-update-dismissals.v1")!)).toEqual([JSON.stringify(["local", "cursor", "2.0.0"])]);
		await userEvent.click(within(menu).getByRole("button", { name: "Dismiss Codex update" }));
		expect(rowNames(menu)).toEqual(["Claude Code"]);
		await userEvent.click(within(menu).getByRole("button", { name: "Dismiss Claude Code update" }));
		await waitFor(() => expect(trigger()).toBeNull());
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
		const { client } = mountMenu();
		await waitFor(() => expect(client.getQueryData(updateAdvisoryQueryKey("codex"))).toBeDefined());
		expect(trigger()).toBeNull();
		await act(async () => pending.resolve({ data: { status: "behind_latest", currentVersion: "1.0.0", latestVersion: "2.0.0", maintenanceMethod: "official-installer" } } as never));
		expect(rowNames(await openMenu())[0]).toBe("Cursor");
	});

	it("keeps other updates when one harness lookup fails", async () => {
		mockStack();
		const original = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation((path, options) => {
			const agentId = (options as { params?: { path?: { agent?: string } } } | undefined)?.params?.path?.agent;
			if (path === "/api/v1/agents/{agent}/update-advisory" && agentId === "cursor") return Promise.reject(new Error("lookup timed out"));
			return (original as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		mountMenu();
		expect(rowNames(await openMenu())).toEqual(["Codex", "Claude Code"]);
	});

	it("preserves only the individually dismissed releases on remount", async () => {
		mockStack();
		const view = mountMenu();
		await userEvent.click(within(await openMenu()).getByRole("button", { name: "Dismiss Cursor update" }));
		view.unmount();
		mountMenu();
		expect(rowNames(await openMenu())).toEqual(["Codex", "Claude Code"]);
	});

	it("hides an update while login is required and shows it after login", async () => {
		const { client } = mountMenu();
		await screen.findByRole("button", { name: TRIGGER });
		act(() => client.setQueryData(["agent-readiness"], { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" })] }));
		await waitFor(() => expect(trigger()).toBeNull());
		expect(useUiStore.getState().settingsModal).toBeNull();
		act(() => client.setQueryData(["agent-readiness"], { agents: [agentReadiness("claude-code", "Claude Code")] }));
		expect(within(await openMenu()).getByRole("button", { name: "Update" })).toBeEnabled();
	});

	it.each([
		{ id: "official-installer", available: true, updateAvailable: false },
		{ id: "official-installer", available: false, updateAvailable: true },
		{ id: "npm", available: true, updateAvailable: true },
	])("hides updates that require manual maintenance (%j)", async (method) => {
		const { client } = mountMenu();
		await screen.findByRole("button", { name: TRIGGER });
		act(() => client.setQueryData(installerQueryKey, [{
			agentId: "claude-code", documentationUrl: "https://code.claude.com/docs/en/setup", methods: [method],
		}]));
		await waitFor(() => expect(trigger()).toBeNull());
	});

	it("excludes hidden harnesses from the count and Update all", async () => {
		mockUpdatableStack();
		const { client } = mountMenu();
		const menu = await openMenu();
		act(() => {
			client.setQueryData(["agent-readiness"], { agents: [
				agentReadiness("claude-code", "Claude Code"),
				agentReadiness("codex", "Codex", { authentication: "unauthorized" }),
				agentReadiness("cursor", "Cursor"),
			] });
			client.setQueryData(installerQueryKey, [
				{ agentId: "claude-code", methods: [{ id: "official-installer", available: true, updateAvailable: true }] },
				{ agentId: "codex", methods: [{ id: "official-installer", available: true, updateAvailable: true }] },
				{ agentId: "cursor", methods: [{ id: "official-installer", available: true, updateAvailable: false }] },
			]);
		});
		await waitFor(() => expect(rowNames(menu)).toEqual(["Claude Code"]));
		expect(screen.getByRole("button", { name: "1 agent update available" })).toBeInTheDocument();
		expect(within(menu).queryByRole("button", { name: "Update all" })).toBeNull();
		await userEvent.click(within(menu).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.objectContaining({ params: { path: { agent: "claude-code" } } })));
	});

	it.each(["/api/v1/agents/installers", "/api/v1/agents/auth-plans"] as const)("waits for %s before showing updates", async (endpoint) => {
		const pending = deferred<never>();
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation((path, options) => path === endpoint
			? pending.promise : (get as (path: string, options: unknown) => Promise<never>)(path, options));
		const { client } = mountMenu();
		await waitFor(() => expect(client.getQueryData(updateAdvisoryQueryKey("claude-code"))).toBeDefined());
		expect(trigger()).toBeNull();
		await act(async () => pending.resolve(await (get as (path: string) => Promise<never>)(endpoint)));
		expect(within(await openMenu()).getByRole("button", { name: "Update" })).toBeEnabled();
	});

	it("keeps the list when an installer refresh fails after it rendered", async () => {
		const { client } = mountMenu();
		const menu = await openMenu();
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation((path, options) => path === "/api/v1/agents/installers"
			? Promise.resolve({ error: { message: "installers unavailable" } } as never)
			: (get as (path: string, options: unknown) => Promise<never>)(path, options));
		await act(async () => { await client.invalidateQueries({ queryKey: installerQueryKey, exact: true }); });
		await waitFor(() => expect(client.getQueryState(installerQueryKey)?.status).toBe("error"));
		expect(within(menu).getByRole("listitem", { name: "Claude Code" })).toBeInTheDocument();
		expect(within(menu).queryByRole("button", { name: "Update" })).toBeNull();
		expect(within(menu).getByRole("button", { name: "View details" })).toBeEnabled();
	});

	it("dismisses the shown release across remounts but shows a newer target", async () => {
		const view = mountMenu();
		await userEvent.click(within(await openMenu()).getByRole("button", { name: "Dismiss Claude Code update" }));
		await waitFor(() => expect(trigger()).toBeNull());
		view.unmount();
		const { client } = mountMenu();
		await waitFor(() => expect(client.getQueryData(updateAdvisoryQueryKey("claude-code"))).toBeDefined());
		expect(trigger()).toBeNull();
		act(() => client.setQueryData(updateAdvisoryQueryKey("claude-code"), { agentId: "claude-code", status: "behind_latest", currentVersion: "2.1.291", latestVersion: "2.1.293", maintenanceMethod: "official-installer" }));
		expect(within(await openMenu()).getByText("v2.1.293")).toBeInTheDocument();
	});

	it.each(["unknown", "current"])("does not notify for %s results", async (status) => {
		const { client } = mountMenu();
		await screen.findByRole("button", { name: TRIGGER });
		act(() => client.setQueryData(updateAdvisoryQueryKey("claude-code"), { agentId: "claude-code", status, currentVersion: "2.1.291", latestVersion: "2.1.292" }));
		await waitFor(() => expect(trigger()).toBeNull());
	});
});

describe("agent updates while sessions run", () => {
	const claudeAdvisory = { agentId: "claude-code", status: "behind_latest", currentVersion: "2.1.291", latestVersion: "2.1.292", maintenanceMethod: "official-installer", source: "official-release", checkedAt: "2026-10-07T00:00:00Z" };
	const sessionsDialog = () => screen.queryByRole("dialog", { name: /Sessions using/ });
	let advisories: Record<string, Record<string, unknown>>;
	let installs: Array<{ agent: string; body: Record<string, unknown> }>;
	let installResponse: (agent: string, body: Record<string, unknown>) => unknown;
	let jobs: Array<Record<string, unknown>>;
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		localStorage.clear();
		useUiStore.setState({ settingsModal: null });
		advisories = { "claude-code": claudeAdvisory };
		installs = [];
		installResponse = (agent) => ({ data: { target: agent, status: "installing" } });
		jobs = [];
		const agents = () => Object.keys(advisories);
		const readiness = () => ({ agents: agents().map((id) => agentReadiness(id as never, id)) });
		vi.spyOn(apiClient, "GET").mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/readiness") return { data: readiness() } as never;
			if (path === "/api/v1/agents/installers") return { data: { agents: agents().map((agentId) => ({ agentId, methods: [{ id: "official-installer", available: true, updateAvailable: true }] })) } } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: agents().map((agentId) => ({ agentId, action: "login", available: true })) } } as never;
			if (path === "/api/v1/agents/{agent}/update-advisory") {
				const agent = (options as { params: { path: { agent: string } } }).params.path.agent;
				return { data: advisories[agent] } as never;
			}
			return { data: undefined } as never;
		});
		vi.spyOn(apiClient, "POST").mockImplementation(async (path, options) => {
			if (path !== "/api/v1/agents/{agent}/install" && path !== "/api/v1/agents/{agent}/install/cancel") return { data: readiness() } as never;
			const request = options as { params: { path: { agent: string } }; body: Record<string, unknown> };
			if (path === "/api/v1/agents/{agent}/install/cancel") {
				jobs = [{ target: request.params.path.agent, status: "interrupted", error: "Update cancelled; sessions were left as they were." }];
				return { data: jobs[0] } as never;
			}
			installs.push({ agent: request.params.path.agent, body: request.body });
			return installResponse(request.params.path.agent, request.body) as never;
		});
	});
	afterEach(() => { cleanup(); vi.restoreAllMocks(); });

	it("updates right away when no session uses the harness", async () => {
		mountMenu();
		await userEvent.click(within(await openMenu()).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(installs).toHaveLength(1));
		expect(sessionsDialog()).toBeNull();
	});

	it("updates a harness open sessions use without asking when it keeps their release", async () => {
		advisories["claude-code"] = { ...claudeAdvisory, inUse: { liveSessions: 2, sessionIds: ["s1", "s2"], level: "safe" } };
		mountMenu();
		await userEvent.click(within(await openMenu()).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(installs).toHaveLength(1));
		expect(sessionsDialog()).toBeNull();
		expect(installs[0].body).toEqual({ method: "official-installer", operation: "update", expectedVersion: "2.1.292" });
	});

	it("updates even when the installer replaces the release open sessions run", async () => {
		advisories["claude-code"] = { ...claudeAdvisory, inUse: { liveSessions: 1, sessionIds: ["s1"], level: "warn" } };
		mountMenu();
		await userEvent.click(within(await openMenu()).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(installs).toHaveLength(1));
		expect(sessionsDialog()).toBeNull();
		expect(installs[0].body).toEqual({ method: "official-installer", operation: "update", expectedVersion: "2.1.292" });
	});

	it("offers the sessions to end instead of an update that cannot run alongside them", async () => {
		advisories["claude-code"] = { ...claudeAdvisory, inUse: { liveSessions: 2, sessionIds: ["s1", "s2"], level: "blocked" } };
		mountMenu();
		await userEvent.click(within(await openMenu()).getByRole("button", { name: "View sessions (2)" }));
		expect(await screen.findByRole("dialog", { name: "Sessions using Claude Code" })).toBeInTheDocument();
		expect(installs).toHaveLength(0);
	});

	it("never offers Retry when the daemon says sessions must end first", async () => {
		installResponse = () => ({ error: { error: "conflict", code: "HARNESS_ACTIVE", message: "end active sessions", requestId: "r1", details: { count: 1, sessionIds: ["s1"], reviewerSessionIds: [], level: "blocked", starting: false } } });
		mountMenu();
		const menu = await openMenu();
		await userEvent.click(within(menu).getByRole("button", { name: "Update" }));
		expect(await within(menu).findByRole("button", { name: "View sessions (1)" })).toBeInTheDocument();
		expect(within(menu).queryByRole("button", { name: "Retry update" })).toBeNull();
		expect(within(menu).queryByRole("alert")).toBeNull();
		expect(within(menu).getByRole("status")).toHaveTextContent("1 open session is using Claude Code.");
	});

	it("retries an update the daemon refused only because the harness was starting", async () => {
		let starting = true;
		installResponse = (agent) => starting
			? { error: { error: "conflict", code: "HARNESS_ACTIVE", message: "starting", requestId: "r1", details: { count: 0, sessionIds: [], reviewerSessionIds: [], level: "blocked", starting: true } } }
			: { data: { target: agent, status: "installing" } };
		mountMenu();
		const menu = await openMenu();
		await userEvent.click(within(menu).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(installs).toHaveLength(1));
		starting = false;
		await userEvent.click(await within(menu).findByRole("button", { name: "Update" }));
		await waitFor(() => expect(installs).toHaveLength(2));
		expect(sessionsDialog()).toBeNull();
	});

	it("checks again after the sessions the daemon named have been seen", async () => {
		installResponse = () => ({ error: { error: "conflict", code: "HARNESS_ACTIVE", message: "end active sessions", requestId: "r1", details: { count: 1, sessionIds: ["s1"], reviewerSessionIds: [], level: "blocked", starting: false } } });
		mountMenu();
		const menu = await openMenu();
		await userEvent.click(within(menu).getByRole("button", { name: "Update" }));
		await userEvent.click(await within(menu).findByRole("button", { name: "View sessions (1)" }));
		expect(await screen.findByRole("dialog", { name: "Sessions using Claude Code" })).toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(sessionsDialog()).toBeNull());
		installResponse = (agent) => ({ data: { target: agent, status: "installing" } });
		await userEvent.click(within(await openMenu()).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(installs).toHaveLength(2));
	});

	it("updates all without asking, skips harnesses whose sessions must end, and summarizes", async () => {
		advisories = {
			"claude-code": { ...claudeAdvisory, inUse: { liveSessions: 2, sessionIds: ["s1", "s2"], level: "warn" } },
			codex: { ...claudeAdvisory, agentId: "codex", inUse: { liveSessions: 2, sessionIds: ["s3", "s4"], level: "blocked" } },
			cursor: { ...claudeAdvisory, agentId: "cursor", inUse: { liveSessions: 1, sessionIds: ["s5"], level: "idle" } },
		};
		mountMenu();
		const menu = await openMenu();
		await userEvent.click(within(menu).getByRole("button", { name: "Update all" }));
		await waitFor(() => expect(installs.map((install) => install.agent).sort()).toEqual(["claude-code", "cursor"]));
		expect(sessionsDialog()).toBeNull();
		expect(await within(menu).findByText("Updating 2 · Skipped Codex (2)")).toBeInTheDocument();
	});

	it("explains a Windows update waiting for idle sessions and lets the user cancel it", async () => {
		jobs = [{ target: "claude-code", status: "waiting_for_idle", startedAt: new Date().toISOString(), waitingOn: [{ sessionId: "s1", reason: "command" }] }];
		mountMenu();
		const menu = await openMenu();
		expect(await within(menu).findByText("Updating when sessions are idle")).toBeInTheDocument();
		await userEvent.click(within(menu).getByRole("button", { name: "Cancel" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install/cancel", { params: { path: { agent: "claude-code" } } }));
		expect(installs).toHaveLength(0);
	});

	it("names the session a long-waiting update waits for", async () => {
		jobs = [{ target: "claude-code", status: "waiting_for_idle", startedAt: new Date(Date.now() - 10 * 60_000).toISOString(), waitingOn: [{ sessionId: "s1", reason: "command" }] }];
		mountMenu();
		const menu = await openMenu();
		expect(await within(menu).findByText("Waiting for s1 (dev server or command running)")).toBeInTheDocument();
		await userEvent.click(within(menu).getByRole("button", { name: "View" }));
		expect(await screen.findByRole("dialog", { name: "Sessions using Claude Code" })).toBeInTheDocument();
	});

	it("keeps a finished update busy until its advisory has refreshed", async () => {
		const refresh = deferred<void>();
		const post = vi.mocked(apiClient.POST);
		const original = post.getMockImplementation() as unknown as (path: unknown, options: unknown) => Promise<never>;
		jobs = [{ target: "claude-code", status: "installing" }];
		mountMenu();
		const menu = await openMenu();
		expect(await within(menu).findByRole("button", { name: /Updating/ })).toBeDisabled();
		post.mockClear();
		post.mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/readiness/ensure") await refresh.promise;
			return original(path, options);
		});
		jobs = [{ target: "claude-code", status: "succeeded" }];
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/agents/readiness/ensure", expect.anything()), { timeout: 3000 });
		// The cached advisory still says behind; the row must not offer Update again.
		expect(screen.queryByRole("button", { name: "Update" })).toBeNull();
		expect(screen.getByRole("button", { name: /Updating/ })).toBeDisabled();
		advisories["claude-code"] = { ...claudeAdvisory, status: "current", currentVersion: "2.1.292" };
		refresh.resolve();
		await waitFor(() => expect(screen.queryByRole("button", { name: /Updating/ })).toBeNull());
		expect(screen.queryByRole("button", { name: "Update" })).toBeNull();
	});

	it("starts a Windows update that waits for idle sessions without asking", async () => {
		advisories["claude-code"] = { ...claudeAdvisory, inUse: { liveSessions: 1, sessionIds: ["s1"], level: "idle" } };
		mountMenu();
		await userEvent.click(within(await openMenu()).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(installs).toHaveLength(1));
		expect(sessionsDialog()).toBeNull();
	});
});
