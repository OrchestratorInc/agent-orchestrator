import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));
vi.mock("../../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../lib/api-client")>()),
	apiClient: api,
}));

import { agentRowState, InstallProblem, InstallProgress, useAgentInstalls, useGhInstall } from "./ToolInstalls";

const plan = (agentId: string, automatic = true) => ({
	agentId,
	automatic,
	available: automatic,
	method: "script",
	methods: [{ id: "script", label: "Script", available: automatic, recommended: true, reinstallAvailable: false, updateAvailable: false }],
	documentationUrl: `https://docs.example/${agentId}`,
	reason: automatic ? undefined : "No installer for this platform.",
});

function wrap(children: ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function GhHarness({ onInstalled = () => undefined }: { onInstalled?: () => void }) {
	const gh = useGhInstall(onInstalled);
	return (
		<div>
			<button type="button" onClick={() => void gh.start()}>install gh</button>
			<span data-testid="busy">{String(gh.view.busy)}</span>
			<span data-testid="ended">{String(gh.view.ended)}</span>
			{gh.view.busy ? <InstallProgress job={gh.view.job} label="GitHub CLI" /> : null}
			{gh.view.ended ? <InstallProblem view={gh.view} /> : null}
		</div>
	);
}

function AgentHarness({ agentId, onInstalled = () => undefined }: { agentId: string; onInstalled?: (id: string) => void }) {
	const agents = useAgentInstalls(onInstalled);
	const view = agents.view(agentId);
	return (
		<div>
			<span data-testid="ready">{String(agents.plansReady)}</span>
			<span data-testid="can">{String(agents.canInstall(agentId))}</span>
			<button type="button" onClick={() => void agents.start(agentId)}>install agent</button>
			<span data-testid="busy">{String(view.busy)}</span>
			{view.busy ? <InstallProgress job={view.job} label={agentId} /> : null}
			{view.ended ? <InstallProblem view={view} /> : null}
		</div>
	);
}

beforeEach(() => {
	api.GET.mockReset().mockResolvedValue({ data: undefined });
	api.POST.mockReset();
});

describe("useGhInstall", () => {
	it("is busy from the click, before the daemon answers", async () => {
		let answer: (value: unknown) => void = () => undefined;
		api.POST.mockReturnValue(new Promise((resolve) => { answer = resolve; }));
		render(wrap(<GhHarness />));
		await userEvent.click(screen.getByRole("button", { name: "install gh" }));
		expect(screen.getByTestId("busy")).toHaveTextContent("true");
		expect(screen.getByRole("progressbar", { name: "GitHub CLI" })).toBeInTheDocument();
		await act(async () => answer({ data: { target: "gh", status: "succeeded" } }));
		expect(screen.getByTestId("busy")).toHaveTextContent("false");
	});

	it("says why when the install can't run here, with the command to run by hand", async () => {
		api.POST.mockResolvedValue({ data: { target: "gh", status: "unsupported", error: "Homebrew is not installed.", command: "brew install gh" } });
		render(wrap(<GhHarness />));
		await userEvent.click(screen.getByRole("button", { name: "install gh" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Homebrew is not installed.");
		expect(screen.getByText("brew install gh")).toBeInTheDocument();
		expect(screen.getByTestId("ended")).toHaveTextContent("true");
	});

	it("shows the command up front where it can't be installed from here", async () => {
		// The daemon's answer on Linux, where installing gh needs sudo.
		api.GET.mockResolvedValue({ data: { target: "gh", status: "unsupported", command: "sudo apt-get install -y gh", error: "AO cannot ask for your administrator password. Run the command below in a terminal." } });
		render(wrap(<GhHarness />));
		expect(await screen.findByRole("alert")).toHaveTextContent("administrator password");
		expect(screen.getByText("sudo apt-get install -y gh")).toBeInTheDocument();
		expect(api.POST).not.toHaveBeenCalled();
	});

	it("shows the request's own error when the install can't start", async () => {
		api.POST.mockResolvedValue({ error: { message: "daemon unavailable" } });
		render(wrap(<GhHarness />));
		await userEvent.click(screen.getByRole("button", { name: "install gh" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("daemon unavailable");
	});

	it("follows a running install to the end", async () => {
		const onInstalled = vi.fn();
		api.POST.mockResolvedValue({ data: { target: "gh", status: "installing", command: "brew install gh", progress: { phase: "downloading", percent: 40 } } });
		api.GET.mockResolvedValue({ data: { target: "gh", status: "succeeded" } });
		render(wrap(<GhHarness onInstalled={onInstalled} />));
		await userEvent.click(screen.getByRole("button", { name: "install gh" }));
		expect(await screen.findByRole("progressbar", { name: "GitHub CLI" })).toHaveAttribute("aria-valuenow", "40");
		await waitFor(() => expect(onInstalled).toHaveBeenCalledOnce(), { timeout: 3000 });
		expect(screen.getByTestId("busy")).toHaveTextContent("false");
	});
});

describe("useAgentInstalls", () => {
	const serve = (plans: unknown[], jobs: unknown[]) => {
		api.GET.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/installers") return { data: { agents: plans } };
			if (path === "/api/v1/agents/install-jobs") return { data: { jobs } };
			return { data: undefined };
		});
	};

	it("installs any agent the daemon has a plan for, through the agent install endpoint", async () => {
		serve([plan("unreal-agent")], []);
		api.POST.mockResolvedValue({ data: { target: "unreal-agent", status: "installing", command: "npm i -g unreal-agent" } });
		render(wrap(<AgentHarness agentId="unreal-agent" />));
		await waitFor(() => expect(screen.getByTestId("can")).toHaveTextContent("true"));
		// Once started, the daemon lists the running job.
		serve([plan("unreal-agent")], [{ target: "unreal-agent", status: "installing", command: "npm i -g unreal-agent" }]);
		await userEvent.click(screen.getByRole("button", { name: "install agent" }));
		expect(api.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", {
			params: { path: { agent: "unreal-agent" } },
			body: { operation: "install" },
		});
		expect(await screen.findByText(/npm i -g unreal-agent/)).toBeInTheDocument();
	});

	it("knows an agent without a one-click installer", async () => {
		serve([plan("cursor", false)], []);
		render(wrap(<AgentHarness agentId="cursor" />));
		await waitFor(() => expect(screen.getByTestId("ready")).toHaveTextContent("true"));
		expect(screen.getByTestId("can")).toHaveTextContent("false");
	});

	it("explains a failed install it started", async () => {
		serve([plan("codex")], []);
		api.POST.mockResolvedValue({ data: { target: "codex", status: "failed", error: "npm exited with 1", command: "npm i -g @openai/codex" } });
		render(wrap(<AgentHarness agentId="codex" />));
		await waitFor(() => expect(screen.getByTestId("can")).toHaveTextContent("true"));
		// The refetch after starting reports the failed job too.
		serve([plan("codex")], [{ target: "codex", status: "failed", error: "npm exited with 1", command: "npm i -g @openai/codex" }]);
		await userEvent.click(screen.getByRole("button", { name: "install agent" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("npm exited with 1");
		expect(screen.getByText("npm i -g @openai/codex")).toBeInTheDocument();
	});

	it("hides the last failure while a retry is out", async () => {
		const failed = { target: "codex", status: "failed", error: "npm exited with 1" };
		serve([plan("codex")], [failed]);
		api.POST.mockResolvedValueOnce({ data: failed });
		render(wrap(<AgentHarness agentId="codex" />));
		await waitFor(() => expect(screen.getByTestId("can")).toHaveTextContent("true"));
		await userEvent.click(screen.getByRole("button", { name: "install agent" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("npm exited with 1");
		let answer: (value: unknown) => void = () => undefined;
		api.POST.mockReturnValueOnce(new Promise((resolve) => { answer = resolve; }));
		await userEvent.click(screen.getByRole("button", { name: "install agent" }));
		expect(screen.getByTestId("busy")).toHaveTextContent("true");
		expect(screen.queryByRole("alert")).toBeNull();
		await act(async () => answer({ data: failed }));
	});

	it("doesn't show an old failure from elsewhere as this page's", async () => {
		serve([plan("codex")], [{ target: "codex", status: "failed", error: "an earlier failure" }]);
		render(wrap(<AgentHarness agentId="codex" />));
		await waitFor(() => expect(screen.getByTestId("can")).toHaveTextContent("true"));
		expect(screen.queryByRole("alert")).toBeNull();
	});
});

describe("agentRowState", () => {
	const agent = (installation: string, auth: string, freshness = "fresh", readiness = "not_ready") => ({
		id: "gemini",
		label: "Gemini CLI",
		effectiveReadiness: readiness,
		installation: { state: installation },
		authentication: { state: auth, freshness },
		usageCount: 0,
	}) as never;

	it("offers sign-in when AO couldn't tell whether an installed agent is signed in", () => {
		// What the daemon reports for Gemini CLI right after installing it.
		expect(agentRowState(agent("installed", "unknown", "stale", "unknown"))).toBe("signin");
		expect(agentRowState(agent("installed", "unauthorized"))).toBe("signin");
	});

	it("waits while a check is still running", () => {
		expect(agentRowState(agent("installed", "unknown", "checking"))).toBe("checking");
		expect(agentRowState(agent("unknown", "unknown"))).toBe("checking");
	});

	it("reads ready and not installed as they are", () => {
		expect(agentRowState(agent("installed", "authorized", "fresh", "ready"))).toBe("ready");
		expect(agentRowState(agent("not_installed", "unknown"))).toBe("missing");
	});
});
