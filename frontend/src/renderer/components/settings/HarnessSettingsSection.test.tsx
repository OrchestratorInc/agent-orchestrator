import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { appI18n } from "../../i18n";
import { agentReadinessQueryKey, useAgentReadinessQuery, type AgentReadiness } from "../../hooks/useAgentReadinessQuery";
import type { TerminalSessionState } from "../../hooks/useTerminalSession";
import { agentReadiness } from "../../test/agent-readiness-fixtures";
import { TooltipProvider } from "../ui/tooltip";
import { HarnessSettingsSection, updateAdvisoryRefreshInterval } from "./HarnessSettingsSection";
import { useHarnessActionRequest } from "../../hooks/useHarnessUpdates";

// Cloud sign-in state for the cloud login rows. Signed out by default, which
// leaves every row on its local-only controls.
const cloudMocks = vi.hoisted(() => ({
	cloudEnabled: false,
	org: undefined as { id: string } | undefined,
	connections: [] as Array<{ provider: string; label?: string; validationState: string }>,
}));

vi.mock("../../hooks/useCloudGate", () => ({
	useCloudGate: () => ({ cloudEnabled: cloudMocks.cloudEnabled, localEnabled: true, client: "" }),
}));

vi.mock("../../hooks/useCloudOrg", () => ({
	useCloudOrg: () => ({ org: cloudMocks.org, isLoading: false, error: null, ready: cloudMocks.org !== undefined }),
}));

vi.mock("../../hooks/useProviderConnections", () => ({
	useProviderConnections: () => ({ data: cloudMocks.connections, isSuccess: true }),
}));

// A connected remote host, off by default; the remote diagnostics test turns it on.
const hostMocks = vi.hoisted(() => {
	const remoteGET = vi.fn();
	const remotePOST = vi.fn();
	// One object, like the real per-host client cache: a fresh one each call
	// would change the component's client every render.
	return { connected: [] as string[], remoteGET, remotePOST, remote: { GET: remoteGET, POST: remotePOST } };
});
vi.mock("../../hooks/useHostConnection", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../hooks/useHostConnection")>()),
	useConnectedHosts: () => hostMocks.connected,
}));
vi.mock("../../lib/host-clients", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../../lib/host-clients")>();
	return {
		...actual,
		clientForHost: (hostId: string) => (hostId === "remote-1" ? (hostMocks.remote as never) : actual.clientForHost(hostId)),
		clientForSessionHost: (hostId?: string) => (hostId === "remote-1" ? (hostMocks.remote as never) : actual.clientForSessionHost(hostId)),
	};
});

const { terminalFocusRequested, terminalStateCallback, terminalMuxFactory } = vi.hoisted(() => ({
	terminalFocusRequested: { value: false },
	terminalMuxFactory: { value: undefined as undefined | (() => unknown) },
	terminalStateCallback: { value: undefined as ((state: TerminalSessionState) => void) | undefined },
}));

vi.mock("../TerminalPane", () => ({
	TerminalPane: ({ focusRequested, onTerminalStateChange, createMux }: { focusRequested?: boolean; onTerminalStateChange?: (state: TerminalSessionState) => void; createMux?: () => unknown }) => {
		terminalMuxFactory.value = createMux;
		terminalFocusRequested.value = focusRequested === true;
		terminalStateCallback.value = onTerminalStateChange;
		return (
			<div data-testid="inline-terminal-body">
				<button onClick={() => onTerminalStateChange?.("exited")}>Complete login terminal</button>
			</div>
		);
	},
}));

function catalogWithInstalled(...installed: string[]) {
	return {
		agents: [
			{ id: "claude-code", label: "Claude Code" },
			{ id: "codex", label: "Codex" },
			{ id: "cursor", label: "Cursor" },
			{ id: "goose", label: "Goose" },
		].map((agent) => ({
			...agent,
			installation: { state: installed.includes(agent.id) ? "installed" : "not_installed", freshness: "fresh", reason: "", reasonCode: "", attemptedAt: null, checkedAt: null },
			authentication: { state: "unknown", freshness: "fresh", reason: "", reasonCode: "", attemptedAt: null, checkedAt: null },
			effectiveReadiness: installed.includes(agent.id) ? "ready" : "not_ready",
			usageCount: 0,
		})),
	};
}

const catalog = catalogWithInstalled("claude-code");
// Login workflows require a confirmed logged-out observation, not "unknown".
catalog.agents[0].authentication.state = "unauthorized";
catalog.agents[0].effectiveReadiness = "not_ready";

const plans = {
	agents: [
		{
			agentId: "claude-code", available: true, automatic: true, method: "homebrew",
			command: "brew install --cask claude-code", documentationUrl: "https://code.claude.com/docs/en/installation",
			methods: [{ id: "homebrew", label: "Homebrew", available: true, recommended: true, command: "brew install --cask claude-code", reinstallAvailable: true, reinstallCommand: "brew reinstall --cask claude-code" }],
		},
		{
			agentId: "codex", available: true, automatic: true, method: "homebrew",
			command: "brew install --cask codex", documentationUrl: "https://github.com/openai/codex",
			methods: [
				{ id: "homebrew", label: "Homebrew", available: true, recommended: true, command: "brew install --cask codex", reinstallAvailable: true, reinstallCommand: "brew reinstall --cask codex" },
				{ id: "npm", label: "npm", available: true, recommended: false, command: "npm install -g @openai/codex", expectedDestination: "/Users/test/.npm/bin", reinstallAvailable: true, reinstallCommand: "npm install -g @openai/codex --force" },
			],
		},
		{
			agentId: "aider", available: true, automatic: true, method: "pipx",
			command: "pipx install aider-chat", documentationUrl: "https://aider.chat/docs/install.html",
			methods: [{ id: "pipx", label: "pipx", available: true, recommended: true, command: "pipx install aider-chat", reinstallAvailable: true, reinstallCommand: "pipx reinstall aider-chat" }],
		},
		{
			agentId: "cursor", available: true, automatic: true, method: "official-installer",
			command: "bash <downloaded from https://cursor.com/install>", documentationUrl: "https://cursor.com/cli",
			methods: [{ id: "official-installer", label: "Official installer", available: true, recommended: true, command: "bash <downloaded from https://cursor.com/install>", reinstallAvailable: false, reinstallReason: "No headless reinstall" }],
		},
		{
			agentId: "goose", available: true, automatic: true, method: "official-installer",
			command: "pwsh.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File <downloaded from https://raw.githubusercontent.com/aaif-goose/goose/main/download_cli.ps1>",
			documentationUrl: "https://goose-docs.ai/docs/getting-started/installation/",
			methods: [{ id: "official-installer", label: "Official installer", available: true, recommended: true, command: "pwsh.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File <downloaded from https://raw.githubusercontent.com/aaif-goose/goose/main/download_cli.ps1>", reinstallAvailable: false, reinstallReason: "No headless reinstall" }],
		},
	],
};

describe("updateAdvisoryRefreshInterval", () => {
	it("retries missing advisory data after five minutes", () => {
		expect(updateAdvisoryRefreshInterval()).toBe(5 * 60_000);
	});

	it.each([
		["unknown", 5 * 60_000],
		["future_status", 5 * 60_000],
		["", 5 * 60_000],
		["current", 60 * 60_000],
		["behind_latest", 60 * 60_000],
	] as const)("uses the expected cadence for %s advisories", (status, expected) => {
		expect(updateAdvisoryRefreshInterval({ agentId: "codex", status, currentVersion: "1.2.3", latestVersion: "1.3.0", checkedAt: "2026-10-06T00:00:00Z" })).toBe(expected);
	});

	it("retries a failed refresh even when an old current result remains cached", () => {
		expect(updateAdvisoryRefreshInterval({ agentId: "codex", status: "current", checkedAt: "2026-10-06T00:00:00Z" }, true)).toBe(5 * 60_000);
	});
});

function ReadinessSelector({ agentId }: { agentId: string }) {
	const readiness = useAgentReadinessQuery();
	return (
		<div data-testid="originating-selector">
			{readiness.data?.agents.find((agent) => agent.id === agentId)?.effectiveReadiness}
		</div>
	);
}

/** The list row for a harness. The first render shows bare labels before the rows mount; wait for the row itself. */
async function findListRow(agentId: string): Promise<HTMLElement> {
	return waitFor(() => {
		const row = document.querySelector<HTMLElement>(`button[data-agent="${agentId}"]`);
		if (!row) throw new Error(`harness row ${agentId} has not rendered`);
		return row;
	});
}

/** Opens a harness from the list (unless its page is already open) and returns its page. */
async function findAgentRow(agentId: string): Promise<HTMLElement> {
	const page = document.querySelector<HTMLElement>(`div[data-agent="${agentId}"]`);
	if (page) return page;
	if (document.querySelector("div[data-agent]")) backToList();
	fireEvent.click(await findListRow(agentId));
	return waitFor(() => {
		const detail = document.querySelector<HTMLElement>(`div[data-agent="${agentId}"]`);
		if (!detail) throw new Error(`harness page ${agentId} has not opened`);
		return detail;
	});
}

/** Returns from a harness page to the list. */
function backToList() {
	fireEvent.click(screen.getByRole("button", { name: "All harnesses" }));
}

function renderSection(focusAgentId?: string, selectorAgentId?: string, initialView?: "local" | "cloud", client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	const view = render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				{selectorAgentId ? <ReadinessSelector agentId={selectorAgentId} /> : null}
				<HarnessSettingsSection focusAgentId={focusAgentId} initialView={initialView} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return { ...view, client };
}

function readyCatalog() {
	const readiness = catalogWithInstalled("claude-code", "codex", "cursor");
	for (const agent of readiness.agents) agent.authentication.state = "authorized";
	return readiness;
}

function mockInstalledOperations(savedMethod?: string, readiness = readyCatalog()) {
	const managedPlans = { agents: plans.agents.map((plan) => ({
		...plan,
		methods: plan.methods.map((method) => ({
			...method,
			updateAvailable: method.id !== "official-installer",
			uninstallAvailable: method.id !== "official-installer",
			updateReason: method.id === "official-installer" ? "Vendor update is not supported." : "",
			uninstallReason: method.id === "official-installer" ? "Vendor removal is not supported." : "",
		})),
	})) };
	let operationJobs = savedMethod ? [{ target: "codex", method: savedMethod, status: "succeeded", startedAt: "2026-09-24T12:00:00Z" }] : [];
	vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
		if (path === "/api/v1/agents/readiness") return { data: readiness } as never;
		if (path === "/api/v1/agents/installers") return { data: managedPlans } as never;
		if (path === "/api/v1/agents/install-jobs") return { data: { jobs: operationJobs } } as never;
		if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "codex", action: "login", launchMode: "terminal", available: true, logoutCommand: "codex logout" }] } } as never;
		if (path === "/api/v1/agents/{agent}/update-advisory") {
			const agentId = (options as { params: { path: { agent: string } } }).params.path.agent;
			return { data: { agentId, status: agentId === "codex" ? "behind_latest" : "current", currentVersion: "1.2.3", latestVersion: "1.3.0", binaryPath: "~/.local/bin/codex", maintenanceMethod: savedMethod, checkedAt: "2026-10-06T00:00:00Z" } } as never;
		}
		return { data: undefined } as never;
	});
	vi.mocked(apiClient.POST).mockImplementation(async (path, options) => {
		if (path === "/api/v1/agents/refresh" || path === "/api/v1/agents/readiness/ensure") return { data: readiness } as never;
		if (path === "/api/v1/agents/{agent}/install") {
			const request = options as { params: { path: { agent: string } }; body: { method: string } };
			const job = { target: request.params.path.agent, method: request.body.method, status: "installing", startedAt: "2026-09-27T12:00:00Z" };
			operationJobs = [job];
			return { data: job } as never;
		}
		return { data: undefined } as never;
	});
}

describe("HarnessSettingsSection", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		terminalFocusRequested.value = false;
		terminalStateCallback.value = undefined;
		cloudMocks.cloudEnabled = false;
		cloudMocks.org = undefined;
		cloudMocks.connections = [];
		useHarnessActionRequest.getState().setRequest(null);
		window.ao!.clipboard.writeText = vi.fn().mockResolvedValue(undefined);
		vi.spyOn(apiClient, "GET").mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.spyOn(apiClient, "POST").mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: catalog } as never;
			if (path === "/api/v1/agents/refresh") return { data: catalog } as never;
			if (path === "/api/v1/agents/{agent}/install") {
				return { data: { target: "codex", status: "failed", error: "npm failed" } } as never;
			}
			return { data: undefined } as never;
		});
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	it("lists versions and status, then opens one harness page with its actions", async () => {
		mockInstalledOperations("npm");
		renderSection();
		const listRow = await findListRow("codex");
		expect(listRow).toHaveAccessibleName("Open Codex details");
		expect(await within(listRow).findByText("v1.2.3")).toBeInTheDocument();
		expect(await within(listRow).findByText("v1.3.0 available")).toBeInTheDocument();
		expect(within(listRow).getByText("Connected")).toBeInTheDocument();
		// The list only navigates; actions live on the harness page.
		expect(within(listRow).queryByRole("button")).toBeNull();
		listRow.focus();
		await userEvent.keyboard("{Enter}");
		const page = await findAgentRow("codex");
		expect(screen.queryByRole("textbox", { name: "Search harnesses" })).toBeNull();
		expect(within(page).getByRole("tab", { name: "Account" })).toHaveAttribute("aria-selected", "true");
		expect(within(page).getByRole("button", { name: "Update" })).toBeEnabled();
		expect(within(page).getByRole("button", { name: "Uninstall" })).toBeEnabled();
		expect(within(page).getByText("Installed version")).toBeInTheDocument();
		// A historical install/check date is not a verified update date.
		expect(within(page).queryByText("Last updated")).toBeNull();
		expect(within(page).queryByRole("combobox")).toBeNull();
		expect(within(page).queryByRole("button", { name: "Refresh login" })).toBeNull();
		backToList();
		expect(await findListRow("claude-code")).toBeInTheDocument();
		expect(screen.getByRole("textbox", { name: "Search harnesses" })).toBeInTheDocument();
	});

	it("lists the models a harness reports, read-only, and refreshes the catalog", async () => {
		mockInstalledOperations("npm");
		const models = {
			agentId: "codex", lastSuccessAt: "2026-10-08T09:00:00Z",
			models: [
				{ id: "gpt-5.3-codex", label: "GPT-5.3 Codex", isDefault: true, efforts: ["low", "medium", "high"] },
				{ id: "gpt-5.3-mini", label: "gpt-5.3-mini", efforts: [] },
			],
		};
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models"
			? { data: models } as never : (get as (path: string, options: unknown) => Promise<never>)(path, options));
		const post = vi.mocked(apiClient.POST).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/models/refresh"
			? { data: { ...models, models: [models.models[0]] } } as never : (post as (path: string, options: unknown) => Promise<never>)(path, options));
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Models" }));
		const list = await within(page).findByRole("list", { name: "Available models" });
		expect(within(list).getByText("GPT-5.3 Codex")).toBeInTheDocument();
		expect(within(list).getByText("gpt-5.3-codex")).toBeInTheDocument();
		expect(within(list).getByText("Default")).toBeInTheDocument();
		expect(within(list).getByText("Effort: low · medium · high")).toBeInTheDocument();
		expect(within(list).getByText("No effort control")).toBeInTheDocument();
		// Read-only: no picker, no default to change here.
		expect(within(page).queryByRole("combobox")).toBeNull();
		expect(within(page).queryByRole("radio")).toBeNull();
		await userEvent.click(within(page).getByRole("button", { name: "Refresh" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/models/refresh", { params: { path: { agent: "codex" }, query: { projectId: undefined, revalidate: undefined } } });
		await waitFor(() => expect(within(page).queryByText("gpt-5.3-mini")).toBeNull());
	});

	it("asks for login before loading models", async () => {
		const readiness = readyCatalog();
		readiness.agents[1].authentication.state = "unauthorized";
		mockInstalledOperations("npm", readiness);
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Models" }));
		expect(within(page).getByText("Log in to Codex to load its models.")).toBeInTheDocument();
		expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path]) => path === "/api/v1/agents/{agent}/models")).toBe(false);
		await userEvent.click(within(page).getByRole("button", { name: "Account" }));
		expect(within(page).getByRole("tab", { name: "Account" })).toHaveAttribute("aria-selected", "true");
	});

	it("shows read-only health, re-checks on request, and copies a health report", async () => {
		mockInstalledOperations("npm");
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("tab", { name: "Health" }));
		expect(await within(page).findByText("v1.2.3")).toBeInTheDocument();
		expect(within(page).getByText("v1.3.0")).toBeInTheDocument();
		expect(within(page).getByText("Update available")).toBeInTheDocument();
		expect(within(page).getByText("npm")).toBeInTheDocument();
		expect(within(page).getByText("Authentication")).toBeInTheDocument();
		expect(within(page).getByText("Executable")).toBeInTheDocument();
		expect(within(page).getByText("~/.local/bin/codex")).toBeInTheDocument();
		// Health never changes the harness: no install, update, login or uninstall here.
		for (const name of ["Install", "Update", "Login", "Uninstall"]) expect(within(page).queryByRole("button", { name })).toBeNull();
		await userEvent.click(within(page).getByRole("button", { name: "Check again" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } });
		await userEvent.click(within(page).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining("Codex health\nInstallation: installed")));
		expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining("Latest version: 1.3.0"));
	});

	it.each(["unauthorized", "configured"])("confirms logout and rechecks readiness without assuming success: %s", async (authStatus) => {
		const readiness = readyCatalog();
		mockInstalledOperations("npm", readiness);
		const post = vi.mocked(apiClient.POST).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/logout") return { data: { agentId: "codex", action: "logout", terminal: { handleId: "logout-codex", title: "Log out of Codex", createdAt: "2026-10-09T00:00:00Z", workingDir: "/tmp" } } } as never;
			if (path === "/api/v1/agents/{agent}/probe") {
				readiness.agents.find(agent => agent.id === "codex")!.authentication.state = authStatus;
				return { data: { agent: { id: "codex", authStatus } } } as never;
			}
			return (post as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: {} } as never);
		renderSection();
		const page = await findAgentRow("codex");
		await userEvent.click(within(page).getByRole("button", { name: "Log out" }));
		const dialog = screen.getByRole("dialog", { name: "Log out of Codex?" });
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/logout", expect.anything());
		await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/logout", expect.anything());
		await userEvent.click(within(page).getByRole("button", { name: "Log out" }));
		await userEvent.click(within(screen.getByRole("dialog", { name: "Log out of Codex?" })).getByRole("button", { name: "Log out" }));
		expect(await within(page).findByText("Log out of Codex")).toBeInTheDocument();
		// A short-lived logout must bypass the retained shell cache, which prunes
		// exited handles before the panel can attach and observe completion.
		expect(terminalMuxFactory.value).toBeTypeOf("function");
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/logout", { params: { path: { agent: "codex" } } });
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/auth", expect.anything());
		await userEvent.click(within(page).getByRole("button", { name: "Complete login terminal" }));
		if (authStatus === "configured") {
			await within(page).findByText("Logout could not be confirmed. The harness may still have another account or an API key configured.", {}, { timeout: 7000 });
			expect(within(page).queryByRole("button", { name: "Login" })).toBeNull();
			return;
		}
		await waitFor(() => expect(within(page).queryByTestId("harness-auth-terminal")).toBeNull());
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } });
		expect(within(page).getByRole("button", { name: "Login" })).toBeEnabled();
	}, 10000);

	it("hides logout when the harness has no native logout command", async () => {
		mockInstalledOperations("npm");
		renderSection();
		expect(within(await findAgentRow("cursor")).queryByRole("button", { name: "Log out" })).toBeNull();
	});

	it("offers to log in again for a signed-in harness", async () => {
		mockInstalledOperations("npm");
		const post = vi.mocked(apiClient.POST).getMockImplementation()!;
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => path === "/api/v1/agents/{agent}/auth" ? { data: {
			agentId: "codex", action: "login", terminal: { handleId: "auth-codex", workingDir: "/tmp", title: "Codex login", createdAt: "2026-10-07T00:00:00Z" },
		} } as never : (post as (path: string, options: unknown) => Promise<never>)(path, options));
		renderSection();
		const page = await findAgentRow("codex");
		expect(await within(page).findByText("Connected")).toBeInTheDocument();
		await userEvent.click(await within(page).findByRole("button", { name: "Log in again" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth", { params: { path: { agent: "codex" } } });
		expect(await screen.findByRole("button", { name: "Complete login terminal" })).toBeInTheDocument();
	});

	it("checks updates from initial readiness without waiting for the page refresh", async () => {
		mockInstalledOperations("npm");
		let resolveRefresh!: (value: unknown) => void;
		const pendingRefresh = new Promise((resolve) => { resolveRefresh = resolve; });
		vi.mocked(apiClient.POST).mockReturnValueOnce(pendingRefresh as never);
		renderSection();
		const row = await findAgentRow("codex");
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/refresh"));
		expect(await within(row).findByRole("button", { name: "Update" })).toBeEnabled();
		expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path]) => path === "/api/v1/agents/{agent}/update-advisory")).toBe(true);
		await act(async () => resolveRefresh({ data: readyCatalog() }));
		expect(await within(row).findByRole("button", { name: "Update" })).toBeEnabled();
	});

	it.each(["current", "failed"])("shows cached Update immediately while revalidating, then handles a %s result", async (result) => {
		mockInstalledOperations("npm");
		let resolveRefresh!: (value: unknown) => void;
		let resolveAdvisory!: (value: unknown) => void;
		let rejectAdvisory!: (reason: Error) => void;
		const pendingRefresh = new Promise((resolve) => { resolveRefresh = resolve; });
		const pendingAdvisory = new Promise((resolve, reject) => { resolveAdvisory = resolve; rejectAdvisory = reject; });
		vi.mocked(apiClient.POST).mockReturnValueOnce(pendingRefresh as never);
		const original = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation((path, options) => {
			if (path === "/api/v1/agents/{agent}/update-advisory") return pendingAdvisory as never;
			return (original as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-readiness"], readyCatalog());
		client.setQueryData(["agent-update-advisory", "local", "codex"], {
			agentId: "codex", status: "behind_latest", currentVersion: "1.2.3", latestVersion: "1.3.0", maintenanceMethod: "npm",
		});
		const view = renderSection(undefined, undefined, undefined, client);
		const row = await findAgentRow("codex");
		expect(await within(row).findByRole("button", { name: "Update" })).toBeEnabled();
		expect(within(row).getByText("v1.3.0 available")).toBeInTheDocument();
		expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path, options]) => path === "/api/v1/agents/{agent}/update-advisory"
			&& (options as { params?: { path?: { agent?: string } } })?.params?.path?.agent === "codex")).toBe(false);
		await act(async () => resolveRefresh({ data: readyCatalog() }));
		await waitFor(() => expect((vi.mocked(apiClient.GET).mock.calls as unknown[][]).some(([path, options]) => path === "/api/v1/agents/{agent}/update-advisory"
			&& (options as { params?: { path?: { agent?: string } } })?.params?.path?.agent === "codex")).toBe(true));
		expect(within(row).getByRole("button", { name: "Update" })).toBeEnabled();
		if (result === "current") {
			await act(async () => resolveAdvisory({ data: { agentId: "codex", status: "current", currentVersion: "1.3.0", latestVersion: "1.3.0", maintenanceMethod: "npm" } }));
			await waitFor(() => expect(within(row).queryByRole("button", { name: "Update" })).toBeNull());
			expect(within(row).queryByText("v1.3.0 available")).toBeNull();
		} else {
			await act(async () => rejectAdvisory(new Error("offline")));
			await waitFor(() => expect(client.getQueryState(["agent-update-advisory", "local", "codex"])?.status).toBe("error"));
			expect(within(row).getByRole("button", { name: "Update" })).toBeEnabled();
			expect(within(row).getByText("v1.3.0 available")).toBeInTheDocument();
		}
		view.unmount();
	});

	it("keeps cached Login and Install usable while page readiness refreshes", async () => {
		mockInstalledOperations("npm");
		vi.mocked(apiClient.POST).mockReturnValueOnce(new Promise(() => {}) as never);
		const readiness = catalogWithInstalled("codex");
		readiness.agents[1].authentication.state = "unauthorized";
		const original = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation((path, options) => path === "/api/v1/agents/readiness"
			? Promise.resolve({ data: readiness }) as never : (original as (path: string, options: unknown) => Promise<never>)(path, options));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-readiness"], readiness);
		const view = renderSection(undefined, undefined, undefined, client);
		const codexRow = await findAgentRow("codex");
		expect(await within(codexRow).findByRole("button", { name: "Login" })).toBeEnabled();
		const claudeRow = await findAgentRow("claude-code");
		expect(await within(claudeRow).findByRole("button", { name: "Install" })).toBeEnabled();
		view.unmount();
	});

	it.each(["unknown", "configured"])("does not force Login before an update for %s auth", async (authentication) => {
		const readiness = readyCatalog();
		readiness.agents[1].authentication.state = authentication;
		mockInstalledOperations("npm", readiness);
		renderSection();
		const row = await findAgentRow("codex");
		expect(await within(row).findByRole("button", { name: "Update" })).toBeEnabled();
		expect(within(row).queryByRole("button", { name: "Login" })).toBeNull();
		expect(within(row).queryByText("Connected")).toBeNull();
	});

	it.each(["future_status", ""])("keeps an observed version without claiming an update for status %s", async (status) => {
		mockInstalledOperations("npm");
		const { client } = renderSection();
		const listRow = await findListRow("codex");
		await within(listRow).findByText("v1.2.3");
		act(() => { client.setQueryData(["agent-update-advisory", "local", "codex"], {
			agentId: "codex", status, currentVersion: "2025.09.25", source: "npm", checkedAt: "2026-10-06T00:00:00Z",
		}); });
		expect(await within(listRow).findByText("2025.09.25")).toBeInTheDocument();
		expect(within(listRow).queryByText(/available$/)).toBeNull();
		const row = await findAgentRow("codex");
		expect(within(row).queryByText(/available$/)).toBeNull();
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
		expect(within(row).getByText("Update availability could not be verified.")).toBeInTheDocument();
	});

	it("uses the detected manager rather than a stale saved method", async () => {
		mockInstalledOperations("unsupported-old-method");
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/update-advisory" && (options as { params?: { query?: { refresh?: boolean } } })?.params?.query?.refresh) return { data: { agentId: "codex", status: "behind_latest", currentVersion: "1.2.3", latestVersion: "1.3.0", source: "homebrew" } } as never;
			return (get as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		const { client } = renderSection();
		await within(await findListRow("codex")).findByText("v1.2.3");
		act(() => { client.setQueryData(["agent-update-advisory", "local", "codex"], {
			agentId: "codex", status: "behind_latest", currentVersion: "1.2.3", latestVersion: "1.3.0", source: "homebrew",
		}); });
		const row = await findAgentRow("codex");
		await waitFor(() => expect(within(row).getByRole("button", { name: "Update" })).toBeEnabled());
		await userEvent.click(within(row).getByRole("button", { name: "Update" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } }, body: { method: "homebrew", operation: "update", expectedVersion: "1.3.0" } });
		expect(within(row).getByRole("status")).toHaveTextContent("Updating");
		expect(screen.queryByRole("dialog")).toBeNull();
	});

	it.each(["already-current", "different-owner", "different-target", "unknown"])("does not run an update when the click-time probe reports %s", async (state) => {
		mockInstalledOperations("npm");
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/update-advisory" && (options as { params?: { query?: { refresh?: boolean } } })?.params?.query?.refresh) return { data: {
				agentId: "codex", status: state === "already-current" ? "current" : state === "unknown" ? "unknown" : "behind_latest",
				currentVersion: state === "already-current" ? "1.3.0" : "1.2.3", latestVersion: state === "different-target" ? "2.0.0" : "1.3.0", maintenanceMethod: state === "different-owner" ? "homebrew" : "npm",
			} } as never;
			return (get as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		renderSection();
		await userEvent.click(await within(await findAgentRow("codex")).findByRole("button", { name: "Update" }));
		await waitFor(() => expect(screen.queryByRole("button", { name: "Updating…" })).toBeNull());
		expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/{agent}/update-advisory", { params: { path: { agent: "codex" }, query: { refresh: true } } });
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("consumes a popup update once through the existing workflow", async () => {
		mockInstalledOperations("npm");
		useHarnessActionRequest.getState().setRequest({ agentId: "codex", latestVersion: "1.3.0", action: "update" });
		renderSection("codex");
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } }, body: { method: "npm", operation: "update", expectedVersion: "1.3.0" } }));
		expect((vi.mocked(apiClient.POST).mock.calls as unknown[][]).filter(([path]) => path === "/api/v1/agents/{agent}/install")).toHaveLength(1);
		expect(useHarnessActionRequest.getState().request).toBeNull();
	});

	it("does not let saved job history override missing current ownership evidence", async () => {
		mockInstalledOperations("npm");
		const get = vi.mocked(apiClient.GET).getMockImplementation()!;
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/update-advisory") return { data: { agentId: "codex", status: "behind_latest", currentVersion: "1.2.3", latestVersion: "1.3.0", source: "official-release" } } as never;
			return (get as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		renderSection();
		const row = await findAgentRow("codex");
		expect(await within(row).findByRole("button", { name: "Update manually" })).toBeEnabled();
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
	});

	it("shows update progress on the page and in the list while it runs", async () => {
		mockInstalledOperations("npm");
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } }, body: { method: "npm", operation: "update", expectedVersion: "1.3.0" } });
		expect(within(row).getByRole("status")).toHaveTextContent("Updating");
		expect(within(row).getByRole("button", { name: "Updating…" })).toBeDisabled();
		expect(within(row).queryByRole("button", { name: "Uninstall" })).toBeNull();
		expect(within(row).queryByRole("button", { name: "Show diagnostics" })).toBeNull();
		backToList();
		expect(within(await findListRow("codex")).getByRole("status")).toHaveTextContent("Updating…");
	});

	it("shows how to remove a vendor install AO cannot remove", async () => {
		const readiness = catalogWithInstalled("cursor");
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: readiness } as never;
			if (path === "/api/v1/agents/installers") return { data: { agents: plans.agents.map((plan) => plan.agentId !== "cursor" ? plan : {
				...plan,
				uninstallGuide: { programPaths: ["~/.local/share/cursor-agent", "~/.local/bin/cursor-agent"], userDataPaths: ["~/.cursor-cli"], editsShellProfile: true, documented: false },
				methods: plan.methods.map((method) => ({ ...method, uninstallAvailable: false, uninstallReason: "No uninstall command." })),
			}) } } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockResolvedValue({ data: readiness } as never);
		renderSection();
		const row = await findAgentRow("cursor");
		expect(within(row).getByText("Remove Cursor yourself")).toBeInTheDocument();
		expect(within(row).getByText("rm -rf ~/.local/share/cursor-agent ~/.local/bin/cursor-agent")).toBeInTheDocument();
		expect(within(row).getByText("rm -rf ~/.cursor-cli")).toBeInTheDocument();
		expect(within(row).getByText("Then delete the PATH line its installer added to your shell profile.")).toBeInTheDocument();
		expect(within(row).getByText("Paths come from its install script; check the vendor guide.")).toBeInTheDocument();
		expect(within(row).getByRole("link", { name: "Uninstall guide" })).toHaveAttribute("href", "https://cursor.com/cli");
		expect(within(row).getByRole("button", { name: "Uninstall" })).toBeDisabled();
		await userEvent.click(within(row).getAllByRole("button", { name: "Copy command" })[0]);
		expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith("rm -rf ~/.local/share/cursor-agent ~/.local/bin/cursor-agent");
	});

	it("explains a harness with no installable method instead of questioning its ownership", async () => {
		const reason = "Built into AO; update AO to update the harness.";
		const readiness = catalogWithInstalled("goose");
		vi.mocked(apiClient.GET).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/readiness") return { data: readiness } as never;
			if (path === "/api/v1/agents/installers") return { data: { agents: [...plans.agents.filter((plan) => plan.agentId !== "goose"), {
				agentId: "goose", available: false, automatic: false, method: "manual", reason,
				methods: [{ id: "manual", label: "Manual", available: false, recommended: true, reason, reinstallAvailable: false, updateAvailable: false, uninstallAvailable: false }],
			}] } } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/{agent}/update-advisory") {
				const agentId = (options as { params: { path: { agent: string } } }).params.path.agent;
				return { data: { agentId, status: "unknown", reason: "version_unparseable", checkedAt: "2026-10-08T00:00:00Z" } } as never;
			}
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockResolvedValue({ data: readiness } as never);
		renderSection();
		const row = await findAgentRow("goose");
		expect(within(row).getAllByText(reason).length).toBeGreaterThan(0);
		expect(within(row).queryByText("Installation method could not be verified. Manage this CLI using its original installer.")).toBeNull();
		expect(within(row).queryByText("Update availability could not be verified.")).toBeNull();
	});

	it("does not guess ownership or offer a method picker for a manual installation", async () => {
		mockInstalledOperations();
		renderSection();
		const row = await findAgentRow("codex");
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		await userEvent.click(within(row).getByRole("button", { name: "Update manually" }));
		expect(openExternal).toHaveBeenCalledWith("https://github.com/openai/codex");
		expect(within(row).getByRole("button", { name: "Uninstall" })).toBeDisabled();
		expect(within(row).getByText("Installation method could not be verified. Manage this CLI using its original installer.")).toBeInTheDocument();
		expect(screen.queryByRole("dialog")).toBeNull();
		expect(within(row).queryByRole("combobox")).toBeNull();
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("uses only a minimal uninstall confirmation and supports cancellation", async () => {
		mockInstalledOperations("npm");
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(within(row).getByRole("button", { name: "Uninstall" }));
		let dialog = screen.getByRole("dialog", { name: "Uninstall Codex?" });
		expect(within(dialog).queryByRole("combobox")).toBeNull();
		expect(within(dialog).queryByRole("checkbox")).toBeNull();
		expect(within(dialog).queryByText("npm")).toBeNull();
		expect(within(dialog).getByRole("button", { name: "Uninstall" })).toBeEnabled();
		await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
		await userEvent.click(within(row).getByRole("button", { name: "Uninstall" }));
		dialog = screen.getByRole("dialog");
		await userEvent.click(within(dialog).getByRole("button", { name: "Uninstall" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } }, body: { method: "npm", operation: "uninstall" } });
		expect(within(row).getByRole("status")).toHaveTextContent("Uninstalling");
	});

	it("never substitutes an available manager for an unsupported saved method", async () => {
		mockInstalledOperations("official-installer");
		renderSection();
		const row = await findAgentRow("codex");
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
		expect(within(row).getByRole("button", { name: "Update manually" })).toBeEnabled();
		expect(within(row).getByRole("button", { name: "Uninstall" })).toBeDisabled();
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("does not ask a harness with not-applicable authentication to log in before updating", async () => {
		const readiness = readyCatalog();
		readiness.agents[1].authentication.state = "not_applicable";
		mockInstalledOperations("npm", readiness);
		renderSection();
		const row = await findAgentRow("codex");
		expect(await within(row).findByRole("button", { name: "Update" })).toBeEnabled();
		expect(within(row).queryByRole("button", { name: "Login" })).toBeNull();
	});

	it.each(["removed", "different-owner", "ownership-unknown"] as const)("blocks an open confirmation when fresh state is %s", async (state) => {
		mockInstalledOperations("npm");
		const { client } = renderSection();
		await within(await findListRow("codex")).findByText("v1.2.3");
		await userEvent.click(within(await findAgentRow("codex")).getByRole("button", { name: "Uninstall" }));
		const dialog = screen.getByRole("dialog");
		act(() => {
			if (state === "removed") client.setQueryData(agentReadinessQueryKey, catalogWithInstalled("claude-code"));
			else client.setQueryData(["agent-update-advisory", "local", "codex"], {
				agentId: "codex", status: "unknown", source: state === "different-owner" ? "homebrew" : undefined,
				reason: state === "ownership-unknown" ? "ownership_unconfirmed" : undefined,
			});
		});
		await waitFor(() => expect(within(dialog).getByRole("button", { name: "Uninstall" })).toBeDisabled());
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("reopens details for a daemon rejection and retries the same update", async () => {
		mockInstalledOperations("npm");
		const post = vi.mocked(apiClient.POST);
		const previous = post.getMockImplementation()!;
		post.mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/install") return { error: { error: "HARNESS_ACTIVE", code: "HARNESS_ACTIVE", message: "End the active Codex session before updating.", requestId: "test-active-session" } } as never;
			return (previous as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		expect(await within(row).findByRole("alert")).toHaveTextContent("End the active Codex session before updating.");
		expect(within(row).getByRole("tab", { name: "Account" })).toHaveAttribute("aria-selected", "true");
		await userEvent.click(within(row).getByRole("button", { name: "Retry update" }));
		const requests = (vi.mocked(apiClient.POST).mock.calls as unknown[][]).filter(([path]) => path === "/api/v1/agents/{agent}/install");
		expect(requests).toHaveLength(2);
		for (const [, options] of requests) expect(options).toMatchObject({ body: { method: "npm", operation: "update" } });
	});

	it("opens a failed update once and does not reopen it while unrelated jobs are polled", async () => {
		mockInstalledOperations("npm");
		const { client } = renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		backToList();
		const failed = { target: "codex", method: "npm", status: "failed", startedAt: "2026-09-27T12:00:00Z", error: "Update failed" };
		act(() => { client.setQueryData(["agent-install-jobs"], [failed]); });
		await waitFor(() => expect(document.querySelector('div[data-agent="codex"]')).not.toBeNull());
		await findAgentRow("claude-code");
		act(() => { client.setQueryData(["agent-install-jobs"], [failed, { target: "goose", status: "installing", output: "Downloading" }]); });
		await waitFor(() => expect(document.querySelector('div[data-agent="claude-code"]')).not.toBeNull());
		expect(document.querySelector('div[data-agent="codex"]')).toBeNull();
		backToList();
		expect(await within(await findListRow("goose")).findByRole("status")).toHaveTextContent("Working…");
	});

	it("prioritizes login, hides uninstall through cleanup, and never auto-updates", async () => {
		const readiness = catalogWithInstalled("codex");
		readiness.agents[1].authentication.state = "unauthorized";
		mockInstalledOperations("npm", readiness);
		let probed = false;
		let resolveClose!: (value: unknown) => void;
		const closing = new Promise((resolve) => { resolveClose = resolve; });
		const post = vi.mocked(apiClient.POST);
		const previous = post.getMockImplementation()!;
		post.mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "codex", action: "login", terminal: { handleId: "auth-codex", workingDir: "/tmp", title: "Codex login", createdAt: "2026-10-07T00:00:00Z" },
			} } as never;
			if (path === "/api/v1/agents/{agent}/probe") {
				probed = true;
				return { data: { agent: { id: "codex", authStatus: "authorized" }, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure" && probed) return { data: {
				agents: [agentReadiness("codex", "Codex", { authentication: "authorized" })],
			} } as never;
			return (previous as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		vi.spyOn(apiClient, "DELETE").mockImplementation(async () => await closing as never);
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
		renderSection();
		const row = await findAgentRow("codex");
		expect(await within(row).findByText("v1.3.0 available")).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
		await userEvent.click(within(row).getByRole("button", { name: "Login" }));
		expect(within(row).getByRole("tab", { name: "Account" })).toHaveAttribute("aria-selected", "true");
		expect(within(row).queryByRole("button", { name: "Uninstall" })).toBeNull();
		await userEvent.click(await screen.findByRole("button", { name: "Complete login terminal" }));
		await waitFor(() => expect(apiClient.DELETE).toHaveBeenCalled());
		expect(within(row).queryByRole("button", { name: "Uninstall" })).toBeNull();
		expect(within(row).queryByRole("button", { name: "Update" })).toBeNull();
		await act(async () => { resolveClose({ data: undefined }); });
		await waitFor(() => expect(within(row).getByRole("button", { name: "Update" })).toBeEnabled());
		expect(within(row).getByRole("button", { name: "Uninstall" })).toBeEnabled();
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("records only a successful update observed here, not job or check timestamps", async () => {
		mockInstalledOperations("npm");
		const { client } = renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Update" }));
		const completed = {
			target: "codex", method: "npm", status: "succeeded", startedAt: "2026-09-27T12:00:00Z",
			finishedAt: "2026-10-07T12:00:00Z", updatedAt: "2026-10-08T12:00:00Z", output: "Update completed",
		};
		const get = vi.mocked(apiClient.GET);
		const previous = get.getMockImplementation()!;
		get.mockImplementation(async (path, options) => path === "/api/v1/agents/install-jobs" ? { data: { jobs: [completed] } } as never : (previous as (path: string, options: unknown) => Promise<never>)(path, options));
		act(() => { client.setQueryData(["agent-install-jobs"], [completed]); });
		await waitFor(() => expect(row.querySelector("time")).toHaveAttribute("datetime", "2026-10-07T12:00:00Z"));
		expect(within(row).queryByRole("button", { name: "Show diagnostics" })).toBeNull();
	});

	it("keeps uninstall hidden until the fresh check after closing login finishes", async () => {
		const readiness = readyCatalog();
		readiness.agents[1].authentication.state = "unauthorized";
		mockInstalledOperations("npm", readiness);
		let resolveProbe!: (value: unknown) => void;
		const probe = new Promise((resolve) => { resolveProbe = resolve; });
		const post = vi.mocked(apiClient.POST);
		const previous = post.getMockImplementation()!;
		post.mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "codex", action: "login", terminal: { handleId: "auth-codex", title: "Codex login", workingDir: "/tmp", createdAt: "2026-10-07T00:00:00Z" },
			} } as never;
			if (path === "/api/v1/agents/{agent}/probe") return await probe as never;
			return (previous as (path: string, options: unknown) => Promise<never>)(path, options);
		});
		vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
		renderSection();
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Login" }));
		await userEvent.click(await within(row).findByRole("button", { name: "Close settings" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } }));
		expect(within(row).queryByRole("button", { name: "Uninstall" })).toBeNull();
		await act(async () => { resolveProbe({ data: { agent: { id: "codex", authStatus: "unauthorized" }, installed: true } }); });
		await waitFor(() => expect(within(row).getByRole("button", { name: "Uninstall" })).toBeEnabled());
	});

	it("keeps displayed versions and maintenance requests scoped to the selected host", async () => {
		mockInstalledOperations("npm");
		hostMocks.connected = ["remote-1"];
		const localGET = vi.mocked(apiClient.GET).getMockImplementation()!;
		hostMocks.remoteGET.mockImplementation(async (path: string, options: unknown) => {
			if (path === "/api/v1/agents/{agent}/update-advisory") return { data: {
				agentId: "codex", status: "behind_latest", currentVersion: "9.4.0", latestVersion: "9.5.0", source: "homebrew",
			} };
			return (localGET as (path: string, options: unknown) => Promise<unknown>)(path, options);
		});
		hostMocks.remotePOST.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/{agent}/install") return { data: { target: "codex", method: "homebrew", status: "installing", startedAt: "2026-10-07T00:00:00Z" } };
			return { data: readyCatalog() };
		});
		try {
			renderSection();
			await within(await findListRow("codex")).findByText("v1.2.3");
			await userEvent.click(screen.getByRole("button", { name: "Host" }));
			await userEvent.click(screen.getByRole("menuitem", { name: "remote-1" }));
			await waitFor(() => expect(within(document.querySelector<HTMLElement>('button[data-agent="codex"]')!).getByText("v9.4.0")).toBeInTheDocument());
			const row = await findAgentRow("codex");
			expect(within(row).queryByText("v1.2.3")).toBeNull();
			expect(within(row).getByText("v9.5.0 available")).toBeInTheDocument();
			await userEvent.click(within(row).getByRole("button", { name: "Update" }));
			expect(hostMocks.remotePOST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } }, body: { method: "homebrew", operation: "update", expectedVersion: "9.5.0" } });
			expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
		} finally {
			hostMocks.connected = [];
			hostMocks.remoteGET.mockReset();
			hostMocks.remotePOST.mockReset();
		}
	});

	it("keeps the local view free of cloud logins", async () => {
		cloudMocks.cloudEnabled = true;
		cloudMocks.org = { id: "org-1" };
		renderSection();

		const claudeRow = await findListRow("claude-code");
		expect(screen.getByRole("tab", { name: "Local" })).toHaveAttribute("aria-selected", "true");
		expect(screen.queryByRole("button", { name: "Login" })).toBeNull();
		expect(within(claudeRow).queryByText(/Cloud/)).toBeNull();
		expect(screen.getByText("Goose")).toBeInTheDocument();
	});

	it("logs cloud harnesses in from the cloud view", async () => {
		cloudMocks.cloudEnabled = true;
		cloudMocks.org = { id: "org-1" };
		cloudMocks.connections = [{ provider: "codex", label: "default", validationState: "valid" }];
		const user = userEvent.setup();
		renderSection();
		await screen.findByText("Goose");

		await user.click(screen.getByRole("tab", { name: "Cloud" }));
		// Only cloud-supported harnesses, with no local install or login controls.
		expect(screen.queryByText("Goose")).toBeNull();
		const claudeRow = screen.getByText("Claude Code").closest('[data-agent="claude-code"]') as HTMLElement;
		expect(within(claudeRow).getByText("Not connected")).toBeInTheDocument();
		// A disclosure and one cloud login action; no local install controls.
		expect(within(claudeRow).getAllByRole("button")).toHaveLength(2);
		expect(within(claudeRow).getByText("API version not reported")).toBeInTheDocument();
		await user.click(within(claudeRow).getByRole("button", { name: "Login" }));
		expect(within(claudeRow).getByTestId("cloud-harness-login")).toBeInTheDocument();
		expect(within(claudeRow).queryByRole("button", { name: "Login" })).toBeNull();
		await user.click(within(claudeRow).getByRole("button", { name: "Cancel" }));
		expect(within(claudeRow).queryByTestId("cloud-harness-login")).toBeNull();

		const codexRow = screen.getByText("Codex").closest('[data-agent="codex"]') as HTMLElement;
		expect(within(codexRow).getByText("Connected")).toBeInTheDocument();
		expect(within(codexRow).queryByRole("button", { name: "Install" })).toBeNull();
		expect(within(codexRow).queryByRole("button", { name: "Refresh login" })).toBeNull();
		await user.click(within(codexRow).getByRole("button", { name: "Expand Codex options" }));
		await user.click(within(codexRow).getByRole("button", { name: "Refresh login" }));
		expect(within(codexRow).getByRole("button", { name: "Log in with ChatGPT" })).toBeInTheDocument();
	});

	it("reads a harness as connected only from its valid default connection", async () => {
		cloudMocks.cloudEnabled = true;
		cloudMocks.org = { id: "org-1" };
		// Several connections for one provider: whichever comes last must not decide.
		cloudMocks.connections = [
			{ provider: "codex", label: "default", validationState: "valid" },
			{ provider: "codex", label: "secondary", validationState: "invalid" },
			{ provider: "claude-code", label: "default", validationState: "invalid" },
			{ provider: "claude-code", label: "secondary", validationState: "valid" },
		];
		renderSection(undefined, undefined, "cloud");

		const codexRow = await findAgentRow("codex");
		expect(within(codexRow).getByText("Connected")).toBeInTheDocument();
		const claudeRow = screen.getByText("Claude Code").closest('[data-agent="claude-code"]') as HTMLElement;
		expect(within(claudeRow).getByText("Not connected")).toBeInTheDocument();
	});

	it("opens straight into the cloud view when asked", async () => {
		cloudMocks.cloudEnabled = true;
		cloudMocks.org = { id: "org-1" };
		renderSection(undefined, undefined, "cloud");

		expect(await screen.findByRole("tab", { name: "Cloud" })).toHaveAttribute("aria-selected", "true");
		expect((await screen.findAllByRole("button", { name: "Login" })).length).toBeGreaterThan(0);
		expect(screen.queryByText("Goose")).toBeNull();
	});

	it("asks to sign in to AO Cloud in the cloud view when signed out", async () => {
		cloudMocks.cloudEnabled = true;
		cloudMocks.org = undefined;
		renderSection(undefined, undefined, "cloud");

		expect(await screen.findByText(/Sign in to AO Cloud/)).toBeInTheDocument();
		expect(screen.queryByText("Claude Code")).toBeNull();
	});

	it("offers no cloud view while the cloud feature is off", async () => {
		cloudMocks.cloudEnabled = false;
		cloudMocks.org = { id: "org-1" };
		cloudMocks.connections = [{ provider: "claude-code", label: "default", validationState: "valid" }];
		renderSection(undefined, undefined, "cloud");

		const claudeRow = await findListRow("claude-code");
		expect(screen.queryByRole("tab", { name: "Cloud" })).toBeNull();
		expect(within(claudeRow).queryByRole("button", { name: "Login" })).toBeNull();
		expect(screen.getByText("Goose")).toBeInTheDocument();
	});

	it("refreshes local readiness in the background without a Refresh login button", async () => {
		const authorized = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "authorized" })] };
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: authorized } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true }] } } as never;
			if (path === "/api/v1/agents/installers") return { data: { agents: [] } } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockResolvedValue({ data: authorized } as never);
		renderSection();

		const row = await findAgentRow("claude-code");
		expect(await within(row).findByText("Connected")).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Refresh login" })).toBeNull();
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/refresh");
		expect(within(row).queryByRole("button", { name: "Login" })).toBeNull();
		expect(within(row).queryByRole("button", { name: "Authorized" })).toBeNull();
	});

	it("offers native login when fx is installed but unauthorized", async () => {
		const fxCatalog = { agents: [{ ...catalogWithInstalled("claude-code").agents[0], id: "fx", label: "fx", authentication: { state: "unauthorized", freshness: "fresh", reason: "fx is not logged in.", reasonCode: "", attemptedAt: null, checkedAt: null } }] };
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: fxCatalog } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "fx", action: "login", launchMode: "terminal", available: true, displayCommand: "fx login", documentationUrl: "https://fx.sh/docs" }] } } as never;
			if (path === "/api/v1/agents/installers") return { data: { agents: [{ agentId: "fx", available: true, automatic: true, method: "official-installer", documentationUrl: "https://fx.sh/docs", methods: [] }] } } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockResolvedValue({ data: fxCatalog } as never);
		renderSection();
		const row = await findAgentRow("fx");
		expect(await within(row).findByRole("button", { name: "Login" })).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Instructions" })).not.toBeInTheDocument();
	});

	it("shows configured MiMo Code without asking for login again", async () => {
		const configured = { agents: [agentReadiness("mimo-code", "MiMo Code", { authentication: "configured" })] };
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: configured } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "mimo-code", action: "login", launchMode: "terminal", available: true }] } } as never;
			if (path === "/api/v1/agents/installers") return { data: { agents: [] } } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		renderSection();
		const row = await findAgentRow("mimo-code");
		expect(await within(row).findByText("Configured")).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Configured" })).toBeNull();
		expect(within(row).queryByRole("button", { name: "Login" })).not.toBeInTheDocument();
	});

	it("offers fx installation while readiness refreshes automatically", async () => {
		const fxCatalog = { agents: [{ ...catalogWithInstalled().agents[0], id: "fx", label: "fx" }] };
		const fxPlan = { agentId: "fx", available: true, automatic: true, method: "official-installer", command: "bash <downloaded from https://fx.sh/setup.sh>", documentationUrl: "https://fx.sh/docs", expectedDestination: "~/.local/bin/fx", methods: [{ id: "official-installer", label: "Official installer", available: true, recommended: true, command: "bash <downloaded from https://fx.sh/setup.sh>", reinstallAvailable: false }] };
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "fx", action: "login", launchMode: "terminal", available: true, displayCommand: "fx login", documentationUrl: "https://fx.sh/docs" }] } } as never;
			if (path === "/api/v1/agents/readiness") return { data: fxCatalog } as never;
			if (path === "/api/v1/agents/installers") return { data: { agents: [fxPlan] } } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/install") return { data: { target: "fx", status: "installing", method: "official-installer" } } as never;
			return { data: fxCatalog } as never;
		});
		renderSection();
		const row = await findAgentRow("fx");
		expect(within(row).queryByRole("button", { name: "Instructions" })).not.toBeInTheDocument();
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/refresh"));
		await userEvent.click(await within(row).findByRole("button", { name: "Install" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "fx" } }, body: { method: "official-installer", operation: "install" } });
	});

	it("opens a targeted harness page, scrolls to it, focuses Install, and highlights it for two seconds once", async () => {
		const scrollIntoView = vi.fn();
		const setTimeoutSpy = vi.spyOn(window, "setTimeout");
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
		const view = renderSection("codex");
		const row = await findAgentRow("codex");
		const install = await within(row).findByRole("button", { name: "Install" });

		await waitFor(() => expect(document.activeElement).toBe(install));
		expect(scrollIntoView).toHaveBeenCalledWith({ behavior: "smooth", block: "start" });
		expect(row).toHaveAttribute("data-focus-highlighted");
		// The page replaces the list, so its search steps aside.
		expect(screen.queryByRole("textbox", { name: "Search harnesses" })).toBeNull();

		const highlightTimeout = setTimeoutSpy.mock.calls.find(([, delay]) => delay === 2_000)?.[0];
		expect(highlightTimeout).toBeTypeOf("function");
		act(() => highlightTimeout?.());
		expect(row).not.toHaveAttribute("data-focus-highlighted");

		view.rerender(
			<QueryClientProvider client={view.client}>
				<HarnessSettingsSection focusAgentId="cursor" />
			</QueryClientProvider>,
		);
		expect(scrollIntoView).toHaveBeenCalledTimes(1);
	});

	it("focuses Login for an installed harness when authentication is required", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") {
				return { data: { plans: [{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true }] } } as never;
			}
			return { data: undefined } as never;
		});
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
		renderSection("claude-code");
		const row = await findAgentRow("claude-code");
		const login = await within(row).findByRole("button", { name: "Login" });

		await waitFor(() => expect(document.activeElement).toBe(login));
	});

	it("falls back to the Account tab when the targeted harness has no primary action", async () => {
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
		renderSection("claude-code");
		const row = await findAgentRow("claude-code");

		await waitFor(() => expect(document.activeElement).toBe(within(row).getByRole("tab", { name: "Account" })));
	});

	it("does not scroll, focus, or highlight for an unknown harness id", async () => {
		const scrollIntoView = vi.fn();
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
		renderSection("not-a-harness");
		await screen.findByText("Codex");
		await waitFor(() => expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/install-jobs"));

		expect(scrollIntoView).not.toHaveBeenCalled();
		expect(document.querySelector("[data-focus-highlighted]")).toBeNull();
		expect(document.activeElement).toBe(document.body);
	});

	it("shows installed harnesses and install actions without authentication UI", async () => {
		renderSection();
		await waitFor(() => expect(screen.getAllByText("Installed").length).toBeGreaterThan(0), { timeout: 10_000 });
		expect(screen.getByText("Codex")).toBeInTheDocument();
		expect(screen.queryByText(/sign in/i)).not.toBeInTheDocument();
	});

	it("shows the authentication action for an installed agent and opens documentation", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") {
				return { data: { plans: [{ agentId: "claude-code", action: "login", launchMode: "documentation", available: true, documentationUrl: "https://example.test/login" }] } } as never;
			}
			return { data: undefined } as never;
		});
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderSection();
		const row = await findAgentRow("claude-code");
		const login = await within(row).findByRole("button", { name: "Login" });
		await userEvent.click(login);
		expect(openExternal).toHaveBeenCalledWith("https://example.test/login");
	});

	it("shows cached readiness while silently refreshing when the page opens", async () => {
		const refreshed = catalogWithInstalled("claude-code", "codex");
		let current = catalog;
		let resolveRefresh!: (value: { data: typeof refreshed }) => void;
		const pendingRefresh = new Promise<{ data: typeof refreshed }>((resolve) => { resolveRefresh = resolve; });
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: current } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/refresh") return await pendingRefresh as never;
			return { data: undefined } as never;
		});

		renderSection();
		const row = await findAgentRow("codex");

		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/refresh"));
		await within(row).findByRole("button", { name: "Install" });
		expect(screen.queryByRole("button", { name: "Refresh harness status" })).not.toBeInTheDocument();
		expect(screen.queryByText("Checking…")).not.toBeInTheDocument();
		await act(async () => {
			current = refreshed;
			resolveRefresh({ data: refreshed });
		});
		await waitFor(() => {
			expect(within(row).getByText("Installed")).toBeInTheDocument();
			expect(within(row).queryByRole("button", { name: "Install" })).toBeNull();
		});
	});

	it("runs a fresh authentication check after terminal completion", async () => {
		const authorized = catalogWithInstalled("claude-code");
		authorized.agents[0].authentication.state = "authorized";
		let probeCalls = 0;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/probe") {
				probeCalls += 1;
				return { data: { agent: { id: "claude-code", label: "Claude Code", authStatus: "authorized" }, supported: true, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure") return { data: authorized } as never;
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "claude-code",
				action: "login",
				guidance: "Complete login in the terminal.",
				terminal: {
					handleId: "auth-terminal-1",
					title: "Claude Code login",
					workingDir: "/tmp",
					createdAt: "2026-09-15T00:00:00Z",
				},
			} } as never;
			return { data: undefined } as never;
		});
		const close = vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		const user = userEvent.setup();

		renderSection();
		const row = await findAgentRow("claude-code");
		const login = await within(row).findByRole("button", { name: "Login" });
		await user.click(login);
		await within(row).findByTestId("inline-terminal-body");
		expect(terminalStateCallback.value).toBeDefined();
		expect(terminalFocusRequested.value).toBe(false);

		act(() => terminalStateCallback.value?.("attached"));
		await waitFor(() => expect(terminalFocusRequested.value).toBe(true));

		act(() => terminalStateCallback.value?.("exited"));
		await waitFor(() => expect(probeCalls).toBe(1));

		await waitFor(() => expect(close).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}", {
			params: { path: { handleId: "auth-terminal-1" } },
		}));
		await waitFor(() => expect(within(row).queryByTestId("inline-terminal-body")).not.toBeInTheDocument());
	});

	// The first check right after a login terminal exits can fail transiently;
	// the panel must not report a completed login as signed out.
	async function loginWithProbeResults(statuses: string[], readinessAfterProbes: number, terminalInput?: string) {
		const authorized = catalogWithInstalled("claude-code");
		authorized.agents[0].authentication.state = "authorized";
		let probeCalls = 0;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/probe") {
				const authStatus = statuses[Math.min(probeCalls, statuses.length - 1)];
				probeCalls += 1;
				return { data: { agent: { id: "claude-code", label: "Claude Code", authStatus }, supported: true, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure") return { data: probeCalls >= readinessAfterProbes ? authorized : catalog } as never;
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "claude-code", action: "login", guidance: "Complete login in the terminal.", terminalInput,
				terminal: { handleId: "auth-terminal-1", title: "Claude Code login", workingDir: "/tmp", createdAt: "2026-09-15T00:00:00Z" },
			} } as never;
			return { data: undefined } as never;
		});
		const close = vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("claude-code");
		await user.click(await within(row).findByRole("button", { name: "Login" }));
		await within(row).findByTestId("inline-terminal-body");
		return { row, close, probeCalls: () => probeCalls, exit: () => act(() => terminalStateCallback.value?.("exited")) };
	}

	it("shows login guidance only when it asks for an action outside the terminal", async () => {
		const { row, exit } = await loginWithProbeResults(["authorized"], Number.POSITIVE_INFINITY);
		expect(within(row).queryByText("Complete login in the terminal.")).toBeNull();
		exit();
		await waitFor(() => expect(within(row).queryByTestId("inline-terminal-body")).not.toBeInTheDocument());
		cleanup();

		const withAction = await loginWithProbeResults(["authorized"], Number.POSITIVE_INFINITY, "/login\r");
		expect(within(withAction.row).getByText("Complete login in the terminal.")).toBeInTheDocument();
	});

	it("re-checks a login whose first post-login check fails transiently", async () => {
		const { row, close, probeCalls, exit } = await loginWithProbeResults(["unauthorized", "authorized"], Number.POSITIVE_INFINITY);
		exit();

		await waitFor(() => expect(within(row).queryByTestId("inline-terminal-body")).not.toBeInTheDocument(), { timeout: 5_000 });
		expect(probeCalls()).toBe(2);
		expect(close).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}", { params: { path: { handleId: "auth-terminal-1" } } });
	});

	it("closes a login panel it could not confirm once the harness reads as logged in", async () => {
		// Every post-login probe misses, but the daemon's readiness later reports
		// the harness logged in.
		const { row, probeCalls, exit } = await loginWithProbeResults(["unauthorized"], 4);
		exit();

		await waitFor(() => expect(probeCalls()).toBe(4), { timeout: 8_000 });
		await waitFor(() => expect(within(row).queryByTestId("inline-terminal-body")).not.toBeInTheDocument(), { timeout: 5_000 });
	}, 15_000);

	it("completes MiMo Code login when the key is configured locally", async () => {
		const initial = { agents: [agentReadiness("mimo-code", "MiMo Code", { authentication: "unauthorized" })] };
		const configured = { agents: [agentReadiness("mimo-code", "MiMo Code", { authentication: "configured" })] };
		let probed = false;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: initial } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "mimo-code", action: "login", launchMode: "terminal", available: true }] } } as never;
			if (path === "/api/v1/agents/installers") return { data: { agents: [] } } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "mimo-code", action: "login", terminal: { handleId: "auth-mimo", projectId: null, sessionId: null, workingDir: "/tmp", title: "MiMo Code login", createdAt: "2026-09-29T00:00:00Z" },
			} } as never;
			if (path === "/api/v1/agents/{agent}/probe") {
				probed = true;
				return { data: { agent: { id: "mimo-code", authStatus: "configured" }, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure") return { data: probed ? configured : initial } as never;
			return { data: undefined } as never;
		});
		const close = vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		renderSection();
		const row = await findAgentRow("mimo-code");
		await userEvent.click(await within(row).findByRole("button", { name: "Login" }));
		await userEvent.click(await within(row).findByRole("button", { name: "Complete login terminal" }));

		await waitFor(() => expect(close).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}", {
			params: { path: { handleId: "auth-mimo" } },
		}));
		expect(await within(row).findByText("Configured")).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Configured" })).toBeNull();
		expect(within(row).queryByRole("button", { name: "Login" })).not.toBeInTheDocument();
		await waitFor(() => expect(within(row).queryByTestId("inline-terminal-body")).not.toBeInTheDocument());
	});

	it("refreshes authentication when the user closes the login terminal", async () => {
		const authorized = catalogWithInstalled("claude-code");
		authorized.agents[0].authentication.state = "authorized";
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/probe") {
				return { data: { agent: { id: "claude-code", label: "Claude Code", authStatus: "authorized" }, supported: true, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure") return { data: authorized } as never;
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "claude-code",
				action: "login",
				guidance: "Complete login in the terminal.",
				terminal: {
					handleId: "auth-terminal-close",
					title: "Claude Code login",
					workingDir: "/tmp",
					createdAt: "2026-09-15T00:00:00Z",
				},
			} } as never;
			return { data: undefined } as never;
		});
		const closeTerminal = vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		const user = userEvent.setup();

		renderSection();
		const row = await findAgentRow("claude-code");
		await user.click(await within(row).findByRole("button", { name: "Login" }));
		await user.click(await within(row).findByRole("button", { name: "Close settings" }));

		await waitFor(() => expect(closeTerminal).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}", {
			params: { path: { handleId: "auth-terminal-close" } },
		}));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", {
			params: { path: { agent: "claude-code" } },
		}));
		await within(row).findByText("Connected");
		expect(within(row).queryByRole("button", { name: "Authorized" })).toBeNull();
	});

	it("uses Configured for a completed setup action", async () => {
		const authorized = catalogWithInstalled("codex");
		authorized.agents[1].authentication.state = "authorized";
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: authorized } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "codex", action: "setup", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: authorized } as never;
			if (path === "/api/v1/agents/{agent}/probe") return { data: { agent: { id: "codex", label: "Codex", authStatus: "authorized" }, supported: true, installed: true } } as never;
			return { data: undefined } as never;
		});

		renderSection();
		const row = await findAgentRow("codex");

		await within(row).findByText("Configured");
	});

	it("does not expose manual readiness controls", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [
				{ agentId: "claude-code", action: "login", launchMode: "terminal", available: true },
			] } } as never;
			return { data: undefined } as never;
		});
		renderSection();
		const row = await findAgentRow("claude-code");
		expect(await within(row).findByRole("button", { name: "Login" })).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Installed" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Refresh harness status" })).not.toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Check login" })).not.toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Check configuration" })).not.toBeInTheDocument();
	});

	it("shows a neutral unknown state without install controls before the first observation", async () => {
		const unknown = catalogWithInstalled();
		unknown.agents[1].installation.state = "unknown";
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: unknown } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [] } } as never;
			return { data: undefined } as never;
		});

		renderSection();
		const row = await findAgentRow("codex");
		expect(await within(row).findByText("Installation status unknown")).toBeInTheDocument();
		expect(within(row).queryByRole("button", { name: "Install" })).not.toBeInTheDocument();
	});

	it("falls back to installer plans when readiness cannot be loaded", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { error: { message: "readiness unavailable" } } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [] } } as never;
			return { data: undefined } as never;
		});

		renderSection();
		const row = await findAgentRow("codex");
		// The readiness query retries once before surfacing its error.
		expect(await within(row).findByRole("button", { name: "Install" }, { timeout: 5_000 })).toBeInTheDocument();
		expect(within(row).queryByText("Installation status unknown")).not.toBeInTheDocument();
	});

	it("falls back to ensuring readiness when the page-open refresh fails", async () => {
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/refresh") return { error: { message: "refresh failed" } } as never;
			if (path === "/api/v1/agents/readiness/ensure") return { data: catalog } as never;
			return { data: undefined } as never;
		});

		renderSection();
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith(
			"/api/v1/agents/readiness/ensure",
			{ body: { agentIds: [], purpose: "display" } },
		));
	});

	it("re-fetches readiness when both the page-open refresh and ensure fail", async () => {
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/refresh") throw new Error("network down");
			if (path === "/api/v1/agents/readiness/ensure") throw new Error("network down");
			return { data: undefined } as never;
		});
		let readinessFetches = 0;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") {
				readinessFetches += 1;
				return { data: catalog } as never;
			}
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});

		renderSection();
		await screen.findByText("Codex");
		await waitFor(() => expect(readinessFetches).toBeGreaterThanOrEqual(2));
	});

	it("sorts harnesses by authentication state while preserving catalog order within each group", async () => {
		const readiness = catalogWithInstalled("claude-code", "codex", "cursor", "goose");
		readiness.agents[0].authentication.state = "unknown";
		readiness.agents[1].authentication.state = "authorized";
		readiness.agents[2].authentication.state = "unauthorized";
		readiness.agents[3].authentication.state = "not_applicable";
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: readiness } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});

		renderSection();

		await waitFor(() => {
			const agentIds = Array.from(document.querySelectorAll<HTMLElement>("[data-agent]"))
				.map((row) => row.dataset.agent);
			expect(agentIds.slice(0, 4)).toEqual(["codex", "goose", "cursor", "claude-code"]);
		});
	});

	it("starts the fixed daemon install route and exposes retry after failure", async () => {
		const user = userEvent.setup();
		renderSection();
		const codexRow = await findAgentRow("codex");
		await user.click(await within(codexRow as HTMLElement).findByRole("button", { name: "Install" }));
		expect(within(codexRow as HTMLElement).queryByRole("button", { name: "Installation method" })).toBeNull();

		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "codex" } },
			body: { method: "homebrew", operation: "install" },
		}));
		await waitFor(() => expect(codexRow).toHaveTextContent("npm failed"));
		expect(codexRow).toHaveTextContent("Retry");
		await user.click(within(codexRow as HTMLElement).getByRole("button", { name: "Retry install" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenLastCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "codex" } },
			body: { method: "homebrew", operation: "install" },
		}));
	});

	it("automatically uses the installer available on the user's machine", async () => {
		const npmOnlyPlans = {
			agents: plans.agents.map((plan) => plan.agentId === "codex" ? {
				...plan,
				method: "npm",
				methods: plan.methods.map((method) => ({
					...method,
					available: method.id === "npm",
					recommended: method.id === "npm",
				})),
			} : plan),
		};
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: npmOnlyPlans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");

		await within(row).findByRole("button", { name: "Install" });
		expect(within(row).queryByRole("combobox", { name: "Installation method" })).not.toBeInTheDocument();
		await user.click(within(row).getByRole("button", { name: "Install" }));

		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "codex" } },
			body: { method: "npm", operation: "install" },
		}));
	});

	it("shows an incompatible OpenCode version reason and keeps installation available", async () => {
		const reason = 'OpenCode 2 requires OpenCode 2, but "/usr/local/bin/opencode" reports OpenCode 1 (1.18.33); select the matching harness or put OpenCode 2 on PATH';
		const mismatch = agentReadiness("opencode-v2", "OpenCode 2", {
			installation: "not_installed",
			authentication: "unknown",
		});
		mismatch.installation.reasonCode = "install_incompatible_version";
		mismatch.installation.reason = reason;
		const readiness = { agents: [mismatch] };
		const installerPlans = { agents: [{
			agentId: "opencode-v2",
			available: true,
			automatic: true,
			method: "npm",
			command: "npm install -g opencode-ai@latest",
			methods: [{ id: "npm", label: "npm", available: true, recommended: true, command: "npm install -g opencode-ai@latest", reinstallAvailable: true }],
		}] };
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: readiness } as never;
			if (path === "/api/v1/agents/installers") return { data: installerPlans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/install") {
				return { data: { target: "opencode-v2", status: "installing", method: "npm" } } as never;
			}
			return { data: readiness } as never;
		});

		renderSection();
		const row = await findAgentRow("opencode-v2");
		expect(await within(row).findByText(reason)).toBeInTheDocument();
		expect(row).not.toHaveTextContent("Installation status unknown");
		const install = within(row).getByRole("button", { name: "Install" });
		expect(install).toBeEnabled();

		await userEvent.click(install);
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "opencode-v2" } },
			body: { method: "npm", operation: "install" },
		}));
	});

	it("does not offer reinstall actions for installed harnesses", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalogWithInstalled("claude-code", "cursor") } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		renderSection();
		const claudeRow = await findAgentRow("claude-code");
		expect(claudeRow).toHaveTextContent("Installed");
		expect(within(claudeRow).queryByRole("button", { name: "Reinstall" })).not.toBeInTheDocument();
		const cursorRow = await findAgentRow("cursor");
		expect(within(cursorRow).queryByRole("button", { name: "Reinstall" })).not.toBeInTheDocument();
		expect(within(cursorRow).queryByRole("button", { name: "Instructions" })).not.toBeInTheDocument();
	});

	it("starts an official vendor installer with one click and no instructions dialog", async () => {
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/install") {
				return { data: { target: "cursor", status: "installing", method: "official-installer" } } as never;
			}
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("cursor");
		await within(row).findByRole("button", { name: "Install" });
		expect(within(row).queryByRole("button", { name: "Instructions" })).not.toBeInTheDocument();

		await user.click(within(row).getByRole("button", { name: "Install" }));

		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "cursor" } },
			body: { method: "official-installer", operation: "install" },
		}));
		expect(row).toHaveTextContent("Installing…");
	});

	it("shows the official Goose installer", async () => {
		renderSection();
		const row = await findAgentRow("goose");
		await within(row).findByRole("button", { name: "Install" });
		expect(within(row).getByRole("button", { name: "Install" })).toBeInTheDocument();
	});

	it("does not treat a historical successful job as current installation inventory", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "succeeded", method: "npm", updatedAt: "2026-08-01T00:00:00Z" }] } } as never;
			return { data: undefined } as never;
		});
		renderSection();
		const row = await findAgentRow("codex");
		await within(row).findByRole("button", { name: "Install" });
		expect(within(row).getByRole("button", { name: "Install" })).toBeEnabled();
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", expect.anything());
	});

	it("probes the installed harness after an observed install completes", async () => {
		let installed = false;
		let installerFetches = 0;
		let jobFetches = 0;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: installed ? catalogWithInstalled("claude-code", "codex") : catalog } as never;
			if (path === "/api/v1/agents/installers") {
				installerFetches += 1;
				return { data: plans } as never;
			}
			if (path === "/api/v1/agents/install-jobs") {
				jobFetches += 1;
				return { data: { jobs: [{
					target: "codex",
					status: jobFetches === 1 ? "installing" : "succeeded",
					method: "npm",
					updatedAt: "2026-08-31T00:00:00Z",
				}] } } as never;
			}
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/probe") {
				installed = true;
				return { data: { agent: { id: "codex", label: "Codex" }, supported: true, installed: true } } as never;
			}
			if (path === "/api/v1/agents/readiness/ensure") {
				return { data: installed ? catalogWithInstalled("claude-code", "codex") : catalog } as never;
			}
			return { data: undefined } as never;
		});
		renderSection();
		const row = await findAgentRow("codex");
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } }), { timeout: 3_000 });
		await waitFor(() => expect(row).toHaveTextContent("Installed"));
		await waitFor(() => expect(installerFetches).toBe(2));
	});

	it.each(["authorized", "unauthorized"] as const)("updates a mounted readiness consumer after installation returns %s", async (authentication) => {
		const initial = { agents: [
			agentReadiness("claude-code", "Claude Code"),
			agentReadiness("codex", "Codex", { installation: "not_installed", authentication: "unknown" }),
		] };
		let probed = false;
		const updated = agentReadiness("codex", "Codex", { authentication });
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: initial } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: probed ? { agents: [updated] } : initial } as never;
			if (path === "/api/v1/agents/{agent}/install") return { data: { target: "codex", status: "succeeded", method: "homebrew", updatedAt: "2026-09-19T00:00:00Z" } } as never;
			if (path === "/api/v1/agents/{agent}/probe") {
				probed = true;
				return { data: { agent: { id: "codex", authStatus: authentication }, installed: true } } as never;
			}
			return { data: undefined } as never;
		});
		const { client } = renderSection(undefined, "codex");
		const selector = screen.getByTestId("originating-selector");
		await waitFor(() => expect(selector).toHaveTextContent("not_ready"));
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Install" }));

		await waitFor(() => expect(row).toHaveTextContent(authentication === "authorized" ? "Connected" : "Installed"));
		await waitFor(() => expect(selector).toHaveTextContent(authentication === "authorized" ? /^ready$/ : /^not_ready$/));
		expect(client.getQueryData<AgentReadiness>(agentReadinessQueryKey)?.agents).toEqual([initial.agents[0], updated]);
		expect(screen.getByTestId("originating-selector")).toBe(selector);
	});

	it("updates a mounted readiness consumer when the Harness authentication terminal completes", async () => {
		const initial = { agents: [
			agentReadiness("claude-code", "Claude Code"),
			agentReadiness("codex", "Codex", { authentication: "unauthorized" }),
		] };
		const authorized = agentReadiness("codex", "Codex");
		let probed = false;
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: initial } as never;
			if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "codex", action: "login", launchMode: "terminal", available: true, logoutCommand: "codex logout" }] } } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: probed ? { agents: [authorized] } : initial } as never;
			if (path === "/api/v1/agents/{agent}/auth") return { data: {
				agentId: "codex", action: "login", terminal: { handleId: "auth-codex", projectId: null, sessionId: null, workingDir: "/tmp", title: "Codex login", createdAt: "2026-09-19T00:00:00Z" },
			} } as never;
			if (path === "/api/v1/agents/{agent}/probe") {
				probed = true;
				return { data: { agent: { id: "codex", authStatus: "authorized" }, installed: true } } as never;
			}
			return { data: undefined } as never;
		});
		vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: undefined } as never);
		Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
		const { client } = renderSection(undefined, "codex");
		const selector = screen.getByTestId("originating-selector");
		await waitFor(() => expect(selector).toHaveTextContent("not_ready"));
		const row = await findAgentRow("codex");
		await userEvent.click(await within(row).findByRole("button", { name: "Login" }));
		await userEvent.click(await screen.findByRole("button", { name: "Complete login terminal" }));

		await waitFor(() => expect(selector).toHaveTextContent(/^ready$/));
		expect(client.getQueryData<AgentReadiness>(agentReadinessQueryKey)?.agents).toEqual([initial.agents[0], authorized]);
		expect(screen.getByTestId("originating-selector")).toBe(selector);
		await waitFor(() => expect(screen.queryByRole("button", { name: "Complete login terminal" })).not.toBeInTheDocument());
	});

	it("admits only one install request per harness while the first POST is pending", async () => {
		let resolveInstall!: (value: unknown) => void;
		let installCalls = 0;
		const pendingInstall = new Promise((resolve) => { resolveInstall = resolve; });
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/install") {
				installCalls += 1;
				return await pendingInstall as never;
			}
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");
		const button = await within(row).findByRole("button", { name: "Install" });
		await user.dblClick(button);
		expect(installCalls).toBe(1);
		resolveInstall({ data: { target: "codex", status: "installing", method: "homebrew" } });
		await waitFor(() => expect(row).toHaveTextContent("Installing…"));
	});

	it("keeps concurrent installs independent with only one spinner status per row", async () => {
		vi.mocked(apiClient.POST).mockImplementation(async (path, options) => {
			if (path === "/api/v1/agents/{agent}/install") {
				const agent = (options as { params: { path: { agent: string } } }).params.path.agent;
				return { data: { target: agent, status: "installing" } } as never;
			}
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const codexPage = await findAgentRow("codex");
		await waitFor(() => expect(within(codexPage).getByRole("button", { name: "Install" })).toBeEnabled());
		await user.click(within(codexPage).getByRole("button", { name: "Install" }));
		expect(await within(codexPage).findByRole("status")).toHaveTextContent("Installing…");
		const aiderPage = await findAgentRow("aider");
		await user.click(within(aiderPage).getByRole("button", { name: "Install" }));
		expect(await within(aiderPage).findByRole("status")).toHaveTextContent("Installing…");
		backToList();

		const codexRow = await findListRow("codex");
		const aiderRow = await findListRow("aider");
		const codexStatus = within(codexRow).getByRole("status");
		const aiderStatus = within(aiderRow).getByRole("status");
		expect(codexStatus.querySelector("svg.animate-spin")).not.toBeNull();
		expect(aiderStatus.querySelector("svg.animate-spin")).not.toBeNull();
		expect(within(codexRow).queryByRole("progressbar")).not.toBeInTheDocument();
		expect(within(aiderRow).queryByRole("progressbar")).not.toBeInTheDocument();
		expect(within(codexRow).getAllByText("Installing…")).toHaveLength(1);
		expect(within(aiderRow).getAllByText("Installing…")).toHaveLength(1);
	});

	it("hydrates interrupted jobs and offers verification", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "interrupted", method: "npm", error: "AO restarted", output: "partial output", expectedDestination: "/Users/test/.npm/bin/codex" }] } } as never;
			return { data: undefined } as never;
		});
		vi.mocked(apiClient.POST).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/{agent}/verify") return { data: { target: "codex", status: "verifying" } } as never;
			if (path === "/api/v1/agents/{agent}/install") return { data: { target: "codex", status: "installing", method: "npm" } } as never;
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");
		await waitFor(() => expect(row).toHaveTextContent("Interrupted"));
		await user.click(within(row).getByRole("button", { name: "Verify again" }));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/verify", { params: { path: { agent: "codex" } } });
		await waitFor(() => expect(row).toHaveTextContent("Verifying…"));
	});

	it("shows and copies daemon diagnostics", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "failed", method: "npm", error: "exit status 1", output: "permission denied", expectedDestination: "/Users/test/.npm/bin/codex" }] } } as never;
			if (path === "/api/v1/usage/sessions/memory") {
				return { data: {
					sessions: [{ sessionId: "s1", rssBytes: 641_728_512, processCount: 3, cpuPercent: 0, sampledAt: "2026-09-22T00:00:00Z", processes: [] }],
					app: { rssBytes: 2 * 1024 ** 3, processCount: 20, cpuPercent: 12 },
					system: { totalBytes: 32 * 1024 ** 3, availableBytes: 4 * 1024 ** 3, swapTotalBytes: 0, swapUsedBytes: 0, swapBytesPerSec: 0, cpuCount: 8, load1: 1.25, cpuPercent: 30, pressureRaw: 5, pressureSource: "psi" },
				} } as never;
			}
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");
		await user.click(await within(row).findByRole("button", { name: "Show diagnostics" }));
		expect(row).toHaveTextContent("permission denied");
		expect(row).toHaveTextContent("/Users/test/.npm/bin/codex");
		await user.click(within(row).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.ao!.clipboard.writeText).toHaveBeenCalled());
		const copied = vi.mocked(window.ao!.clipboard.writeText).mock.calls.at(-1)![0] as string;
		// The machine the install died on is part of the report.
		expect(copied).toContain("permission denied");
		expect(copied).toContain("Memory: AO 2.0 GB · available 4.0 GB of 32.0 GB");
		expect(copied).toContain("CPU: 30% of 8 cores · load 1.25");
		expect(copied).toContain("Live sessions: 1 · 612 MB");
	});

	it("copies the remote host's machine numbers, not this computer's", async () => {
		hostMocks.connected = ["remote-1"];
		try {
			const memory = (availableGB: number, cores: number) => ({ data: {
				sessions: [],
				app: { rssBytes: 1024 ** 3, processCount: 4, cpuPercent: 2 },
				system: { totalBytes: 64 * 1024 ** 3, availableBytes: availableGB * 1024 ** 3, swapTotalBytes: 0, swapUsedBytes: 0, swapBytesPerSec: 0, cpuCount: cores, load1: 0.5, cpuPercent: 10, pressureRaw: 0, pressureSource: "psi" },
			} });
			hostMocks.remoteGET.mockImplementation(async (path: string) => {
				if (path === "/api/v1/agents/readiness") return { data: catalog };
				if (path === "/api/v1/agents/installers") return { data: plans };
				if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "failed", method: "npm", error: "exit status 1", output: "remote failure" }] } };
				if (path === "/api/v1/usage/sessions/memory") return memory(48, 32);
				return { data: undefined };
			});
			// Readiness refreshes and installs answer the same as this computer's.
			hostMocks.remotePOST.mockImplementation((path: never, init: never) => apiClient.POST(path, init));
			vi.mocked(apiClient.GET).mockImplementation(async (path) => {
				if (path === "/api/v1/usage/sessions/memory") return memory(3, 8) as never;
				return { data: undefined } as never;
			});
			const user = userEvent.setup();
			const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			render(
				<QueryClientProvider client={client}>
					<TooltipProvider>
						<HarnessSettingsSection hostId="remote-1" />
					</TooltipProvider>
				</QueryClientProvider>,
			);
			const row = await findAgentRow("codex");
			await user.click(await within(row).findByRole("button", { name: "Show diagnostics" }));
			await user.click(within(row).getByRole("button", { name: "Copy diagnostics" }));
			await waitFor(() => expect(window.ao!.clipboard.writeText).toHaveBeenCalled());
			const copied = vi.mocked(window.ao!.clipboard.writeText).mock.calls.at(-1)![0] as string;
			expect(copied).toContain("remote failure");
			expect(copied).toContain("available 48.0 GB of 64.0 GB");
			expect(copied).toContain("of 32 cores");
			expect(apiClient.GET).not.toHaveBeenCalledWith("/api/v1/usage/sessions/memory", expect.anything());
		} finally {
			hostMocks.connected = [];
			hostMocks.remoteGET.mockReset();
			hostMocks.remotePOST.mockReset();
		}
	});

	it("leaves load out of copied diagnostics on a platform with no load average", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "failed", method: "npm", error: "exit status 1", output: "permission denied", expectedDestination: "/Users/test/.npm/bin/codex" }] } } as never;
			if (path === "/api/v1/usage/sessions/memory") {
				return { data: {
					sessions: [],
					app: { rssBytes: 2 * 1024 ** 3, processCount: 20, cpuPercent: 12 },
					// Windows reports load1 = -1 to mean "no such concept here", not 0.
					system: { totalBytes: 32 * 1024 ** 3, availableBytes: 4 * 1024 ** 3, swapTotalBytes: 0, swapUsedBytes: 0, swapBytesPerSec: 0, cpuCount: 8, load1: -1, cpuPercent: 30, pressureRaw: 5, pressureSource: "available_pct" },
				} } as never;
			}
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");
		await user.click(await within(row).findByRole("button", { name: "Show diagnostics" }));
		await user.click(within(row).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.ao!.clipboard.writeText).toHaveBeenCalled());
		const copied = vi.mocked(window.ao!.clipboard.writeText).mock.calls.at(-1)![0] as string;
		expect(copied).toContain("CPU: 30% of 8 cores");
		expect(copied).not.toContain("load");
	});

	it("still copies diagnostics when the host cannot be measured", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [{ target: "codex", status: "failed", method: "npm", error: "exit status 1", output: "permission denied" }] } } as never;
			if (path === "/api/v1/usage/sessions/memory") return { error: { code: "MEMORY_UNSUPPORTED" } } as never;
			return { data: undefined } as never;
		});
		const user = userEvent.setup();
		renderSection();
		const row = await findAgentRow("codex");
		await user.click(await within(row).findByRole("button", { name: "Show diagnostics" }));
		await user.click(within(row).getByRole("button", { name: "Copy diagnostics" }));
		await waitFor(() => expect(window.ao!.clipboard.writeText).toHaveBeenCalled());
		const copied = vi.mocked(window.ao!.clipboard.writeText).mock.calls.at(-1)![0] as string;
		expect(copied).toContain("permission denied");
		expect(copied).not.toContain("Machine");
	});

	it("surfaces install job polling failures", async () => {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/agents/readiness") return { data: catalog } as never;
			if (path === "/api/v1/agents/installers") return { data: plans } as never;
			if (path === "/api/v1/agents/install-jobs") return { error: { error: { message: "Could not poll installation status." } } } as never;
			return { data: undefined } as never;
		});
		renderSection();
		expect(await screen.findByText("Could not poll installation status.")).toBeInTheDocument();
	});
});
