import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));
vi.mock("../../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../lib/api-client")>()),
	apiClient: api,
}));
const auth = vi.hoisted(() => ({ probe: vi.fn() }));
const terminals = vi.hoisted(() => ({ close: vi.fn() }));
vi.mock("../settings/harness/HarnessAuthTerminalPanel", async (importOriginal) => ({
	...(await importOriginal<typeof import("../settings/harness/HarnessAuthTerminalPanel")>()),
	closeAuthTerminal: terminals.close,
}));
vi.mock("../../hooks/useAgentAuth", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../hooks/useAgentAuth")>()),
	probeAgentAuth: auth.probe,
}));

import { resetOnboardingTelemetryForTests, startOnboardingAction } from "../../lib/onboarding-telemetry";
import { agentRowState, InstallProblem, InstallProgress, useAgentInstalls, useGhInstall, closeLoginWorkflow, LOGIN_LIFETIME_MS, LOGIN_VERIFY_MS, useLoginLifetime, useOnboardingLogin, useVerifyWhileSigningIn, verifyAgentSignIn } from "./ToolInstalls";

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
	// Attempts the funnel holds open between tests would change what a mount resumes.
	resetOnboardingTelemetryForTests();
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

	it("ignores a mount lookup that answers after a new install started", async () => {
		let answerLookup: (value: unknown) => void = () => undefined;
		api.GET.mockReturnValueOnce(new Promise((resolve) => { answerLookup = resolve; }));
		api.POST.mockResolvedValue({ data: { target: "gh", status: "installing", command: "brew install gh" } });
		render(wrap(<GhHarness />));
		await userEvent.click(screen.getByRole("button", { name: "install gh" }));
		expect(screen.getByTestId("busy")).toHaveTextContent("true");
		// An older job's failure, from before this install, arrives late.
		await act(async () => answerLookup({ data: { target: "gh", status: "failed", error: "An earlier install failed." } }));
		expect(screen.getByTestId("busy")).toHaveTextContent("true");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("follows an install started before the page was left, back to its end", async () => {
		// Started on an earlier visit to the page (Back unmounted it).
		startOnboardingAction("gh_install", { target: "gh", step: "tools" });
		api.GET.mockResolvedValue({ data: { target: "gh", status: "failed", error: "brew failed." } });
		render(wrap(<GhHarness />));
		expect(await screen.findByRole("alert")).toHaveTextContent("brew failed.");
		expect(screen.getByTestId("ended")).toHaveTextContent("true");
		expect(api.POST).not.toHaveBeenCalled();
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

describe("useGhInstall polling", () => {
	afterEach(() => vi.useRealTimers());

	it("starts one install however fast the clicks come", async () => {
		api.POST.mockReturnValue(new Promise(() => undefined));
		render(wrap(<GhHarness />));
		const button = screen.getByRole("button", { name: "install gh" });
		await act(async () => {
			button.click();
			button.click();
		});
		expect(api.POST).toHaveBeenCalledOnce();
	});

	it("rides out a failed poll and stops waiting once the daemon stays gone", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		api.POST.mockResolvedValue({ data: { target: "gh", status: "installing", command: "brew install gh" } });
		render(wrap(<GhHarness />));
		await act(async () => screen.getByRole("button", { name: "install gh" }).click());
		// The mount's own check answered; every poll from here on is refused.
		api.GET.mockRejectedValue(new TypeError("Failed to fetch"));
		await act(async () => { await vi.advanceTimersByTimeAsync(3_000); });
		expect(screen.getByTestId("busy")).toHaveTextContent("true");
		await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
		expect(screen.getByTestId("busy")).toHaveTextContent("false");
		expect(screen.getByRole("alert")).toHaveTextContent("The install didn't finish.");
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

describe("InstallProgress (compact)", () => {
	it("keeps what is running readable: on hover and for screen readers", () => {
		const job = { target: "gh", status: "installing", command: "sudo apt-get install -y gh", progress: { phase: "downloading", percent: 40, detail: "Get:1 http://archive.ubuntu.com gh" } } as never;
		render(wrap(<InstallProgress compact job={job} label="GitHub CLI" />));
		expect(screen.getByRole("progressbar", { name: "GitHub CLI" })).toHaveAttribute("aria-valuenow", "40");
		const status = screen.getByRole("status");
		expect(status).toHaveTextContent("sudo apt-get install -y gh");
		expect(status.getAttribute("title")).toContain("sudo apt-get install -y gh");
		expect(status.getAttribute("title")).toContain("Get:1 http://archive.ubuntu.com gh");
	});
});

describe("InstallProblem", () => {
	it("opens the installer's output when the error alone doesn't say why", async () => {
		const view = { busy: false, ended: true, problem: "exit status 1", job: { target: "gh", status: "failed", error: "exit status 1", output: "Error: Failed to download resource \"gh\"\nTry again with --verbose", command: "brew install gh" } } as never;
		render(wrap(<InstallProblem view={view} />));
		expect(screen.queryByText(/Failed to download resource/)).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Show output" }));
		expect(screen.getByText(/Failed to download resource/)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Hide output" }));
		expect(screen.queryByText(/Failed to download resource/)).toBeNull();
	});

	it("offers the harness's other installers in Settings", async () => {
		const otherWays = vi.fn();
		render(wrap(<InstallProblem view={{ busy: false, ended: true, problem: "Forbidden" }} otherWays={otherWays} />));
		await userEvent.click(screen.getByRole("button", { name: "Other ways to install" }));
		expect(otherWays).toHaveBeenCalledOnce();
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

describe("verifyAgentSignIn", () => {
	it("verifies sign-in afresh before reading readiness, so a login that just finished counts", async () => {
		// Readiness last saw Cursor signed out; the probe is what sees the new login.
		let signedIn = false;
		auth.probe.mockReset().mockImplementation(async () => {
			signedIn = true;
			return {};
		});
		const order: string[] = [];
		const refresh = vi.fn(async () => {
			order.push(signedIn ? "read: signed in" : "read: signed out");
		});
		await verifyAgentSignIn(new QueryClient(), ["cursor"], refresh);
		expect(auth.probe).toHaveBeenCalledWith("cursor");
		expect(refresh).toHaveBeenCalledWith(["cursor"]);
		expect(order).toEqual(["read: signed in"]);
	});

	it("still reads readiness when the probe can't complete", async () => {
		auth.probe.mockReset().mockRejectedValue(new Error("daemon unavailable"));
		const refresh = vi.fn(async () => undefined);
		await verifyAgentSignIn(new QueryClient(), ["codex"], refresh);
		expect(refresh).toHaveBeenCalledWith(["codex"]);
	});
});

describe("useOnboardingLogin", () => {
	const login = (handleId: string) => ({
		agentId: "cursor",
		action: "login",
		terminal: { handleId, title: "Sign in to Cursor", createdAt: "2026-11-15T00:00:00Z" },
		guidance: "",
		phase: "running",
		startedAt: 0,
	}) as never;

	beforeEach(() => terminals.close.mockReset().mockResolvedValue(undefined));

	it("keeps a login across the step's comings and goings, closing it only when the flow ends", () => {
		// The flow owns the hook; Your tools reading it can unmount and remount (Back, then forward).
		const flow = renderHook(() => useOnboardingLogin());
		act(() => flow.result.current[1](login("term-1")));
		flow.rerender();
		expect(flow.result.current[0]).toMatchObject({ terminal: { handleId: "term-1" } });
		expect(terminals.close).not.toHaveBeenCalled();
		flow.unmount();
		expect(terminals.close).toHaveBeenCalledWith("term-1");
	});

	it("closes a login that comes back after the flow has ended", () => {
		const flow = renderHook(() => useOnboardingLogin());
		const setWorkflow = flow.result.current[1];
		flow.unmount();
		setWorkflow(login("late"));
		expect(terminals.close).toHaveBeenCalledWith("late");
	});
});

describe("useVerifyWhileSigningIn", () => {
	afterEach(() => vi.useRealTimers());

	it("notices a login that finishes while its terminal stays open, then stops checking", async () => {
		vi.useFakeTimers();
		// Droid-style: the CLI signs in inside the terminal, which never exits.
		let signedIn = false;
		const verify = vi.fn(async () => undefined);
		const hook = renderHook(({ done }) => useVerifyWhileSigningIn("droid", done, verify), { initialProps: { done: false } });
		await act(async () => { await vi.advanceTimersByTimeAsync(LOGIN_VERIFY_MS); });
		expect(verify).toHaveBeenLastCalledWith(["droid"]);
		// The credentials change mid-login; the next check sees it and the row turns ready.
		signedIn = true;
		await act(async () => { await vi.advanceTimersByTimeAsync(LOGIN_VERIFY_MS); });
		expect(verify).toHaveBeenCalledTimes(2);
		hook.rerender({ done: signedIn });
		await act(async () => { await vi.advanceTimersByTimeAsync(LOGIN_VERIFY_MS * 3); });
		expect(verify).toHaveBeenCalledTimes(2);
	});

	it("waits for a slow check before starting the next", async () => {
		vi.useFakeTimers();
		let finish: () => void = () => undefined;
		const verify = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
		renderHook(() => useVerifyWhileSigningIn("qwen", false, verify));
		await act(async () => { await vi.advanceTimersByTimeAsync(LOGIN_VERIFY_MS * 4); });
		expect(verify).toHaveBeenCalledOnce();
		await act(async () => { finish(); await vi.advanceTimersByTimeAsync(LOGIN_VERIFY_MS); });
		expect(verify).toHaveBeenCalledTimes(2);
	});
});

describe("harness login lifetime and cleanup", () => {
	const login = (handleId: string, startedAt = Date.now()) => ({
		agentId: "cursor",
		action: "login",
		terminal: { handleId, title: "Sign in to Cursor", createdAt: "2026-11-15T00:00:00Z" },
		guidance: "",
		phase: "running",
		startedAt,
	}) as never;
	const messages = { timedOut: "Authentication timed out. Retry to continue.", failed: "Authentication failed." };

	beforeEach(() => terminals.close.mockReset().mockResolvedValue(undefined));
	afterEach(() => vi.useRealTimers());

	it("keeps a login whose terminal won't close, offering Retry, instead of dropping it", async () => {
		terminals.close.mockRejectedValueOnce(new Error("daemon unavailable"));
		const setWorkflow = vi.fn();
		expect(await closeLoginWorkflow(login("term-1"), setWorkflow, messages.failed)).toBe(false);
		expect(setWorkflow).toHaveBeenCalledWith(expect.objectContaining({ phase: "cleanup_failed", reason: "daemon unavailable" }));
		// Retry (the panel calls close again) clears it once the terminal closes.
		setWorkflow.mockClear();
		expect(await closeLoginWorkflow(login("term-1"), setWorkflow, messages.failed)).toBe(true);
		expect(setWorkflow).toHaveBeenCalledWith(null);
	});

	it("ends a login left open for the lifetime, then verifies sign-in once more", async () => {
		vi.useFakeTimers();
		const setWorkflow = vi.fn();
		const onTimedOut = vi.fn();
		renderHook(() => useLoginLifetime(login("term-2"), setWorkflow, messages, onTimedOut));
		await act(async () => { await vi.advanceTimersByTimeAsync(LOGIN_LIFETIME_MS - 1_000); });
		expect(terminals.close).not.toHaveBeenCalled();
		await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
		expect(terminals.close).toHaveBeenCalledWith("term-2");
		expect(setWorkflow).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "timed_out", reason: messages.timedOut }));
		expect(onTimedOut).toHaveBeenCalledWith("cursor");
	});

	it("offers Retry when the timed-out login's terminal won't close", async () => {
		vi.useFakeTimers();
		terminals.close.mockRejectedValueOnce(new Error("still running"));
		const setWorkflow = vi.fn();
		renderHook(() => useLoginLifetime(login("term-3", Date.now() - LOGIN_LIFETIME_MS), setWorkflow, messages, vi.fn()));
		await act(async () => { await vi.advanceTimersByTimeAsync(0); });
		expect(setWorkflow).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "cleanup_failed", reason: "still running" }));
	});
});
