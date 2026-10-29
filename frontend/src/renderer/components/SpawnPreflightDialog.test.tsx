import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SpawnPreflightInput, SpawnPreflightReport } from "../lib/spawn-preflight";

const mocks = vi.hoisted(() => ({
	check: vi.fn(),
	setAgent: vi.fn(),
	setBranch: vi.fn(),
	clipboard: vi.fn(),
	openExternal: vi.fn(),
	install: vi.fn(),
	startAuth: vi.fn(),
	closeTerminal: vi.fn(),
}));

vi.mock("../lib/spawn-preflight", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/spawn-preflight")>()),
	checkSpawnPreflight: mocks.check,
	runPreflightInstall: mocks.install,
	setProjectOrchestratorAgent: mocks.setAgent,
	setProjectDefaultBranch: mocks.setBranch,
}));

vi.mock("../hooks/useAgentReadinessQuery", () => ({
	useEnsureAgentReadiness: () => true,
	useAgentReadinessQuery: () => ({
		data: {
			agents: [
				{ id: "codex", label: "Codex", effectiveReadiness: "not_ready" },
				{ id: "claude-code", label: "Claude Code", effectiveReadiness: "ready" },
				{ id: "opencode", label: "opencode", effectiveReadiness: "unknown" },
			],
		},
	}),
}));

vi.mock("../hooks/useAgentAuth", () => ({
	useStartAgentAuth: () => ({ mutateAsync: mocks.startAuth }),
}));

// The real panel attaches a terminal; this stand-in exposes the exit signal.
vi.mock("./settings/harness/HarnessAuthTerminalPanel", () => ({
	closeAuthTerminal: mocks.closeTerminal,
	HarnessAuthTerminalPanel: ({ workflow, onTerminalState }: { workflow: { terminal: { title: string } }; onTerminalState: (state: string) => void }) => (
		<div>
			<p>{workflow.terminal.title}</p>
			<button type="button" onClick={() => onTerminalState("exited")}>Exit login</button>
		</div>
	),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: { clipboard: { writeText: mocks.clipboard }, app: { openExternal: mocks.openExternal } },
}));

import { SpawnPreflightDialog } from "./SpawnPreflightDialog";
import { useSpawnPreflightStore } from "../lib/spawn-preflight";

const readyReport: SpawnPreflightReport = { ready: true, canProceed: true, kind: "orchestrator", harness: "claude-code", checks: [] };

function openDialog(report: SpawnPreflightReport, input: SpawnPreflightInput = { projectId: "p1", kind: "orchestrator" }) {
	const resolve = vi.fn();
	const reject = vi.fn();
	act(() => useSpawnPreflightStore.getState().open({ input, report, resolve, reject }));
	render(<SpawnPreflightDialog />);
	return { resolve, reject };
}

beforeEach(() => {
	vi.clearAllMocks();
	useSpawnPreflightStore.setState({ pending: null });
	mocks.clipboard.mockResolvedValue(undefined);
	mocks.setAgent.mockResolvedValue(undefined);
	mocks.setBranch.mockResolvedValue(undefined);
	mocks.closeTerminal.mockResolvedValue(undefined);
});

describe("SpawnPreflightDialog", () => {
	// The daemon resolves each OS's fix; the dialog must show exactly that
	// command and let the user copy it on every platform.
	it.each([
		["Windows", "agent_not_installed", "Codex is not installed", "npm install -g @openai/codex"],
		["macOS", "runtime_prerequisite_missing", "tmux is not installed", "brew install tmux"],
		["Linux", "agent_unauthorized", "Codex is not signed in", "codex login"],
	] as const)("shows and copies the %s fix command", async (_os, code, label, command) => {
		const user = userEvent.setup();
		const report: SpawnPreflightReport = {
			ready: false, canProceed: false, kind: "orchestrator", harness: "codex",
			checks: [{ code, blocking: true, command, documentationUrl: "https://example.test/setup" }],
		};
		openDialog(report);
		expect(screen.getByRole("dialog", { name: "Finish setup to start the orchestrator" })).toBeInTheDocument();
		expect(screen.getByText(label)).toBeInTheDocument();
		expect(screen.getByText(command)).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Copy command" }));
		expect(mocks.clipboard).toHaveBeenCalledWith(command);
		await user.click(screen.getByRole("button", { name: /Setup guide/ }));
		expect(mocks.openExternal).toHaveBeenCalledWith("https://example.test/setup");
	});

	it("re-checks with the copied fix as the method and starts once clear", async () => {
		const user = userEvent.setup();
		mocks.check.mockResolvedValue(readyReport);
		const { resolve } = openDialog({
			ready: false, canProceed: false, kind: "orchestrator", harness: "codex",
			checks: [{ code: "agent_not_installed", blocking: true, command: "npm install -g @openai/codex" }],
		});
		await user.click(screen.getByRole("button", { name: "Copy command" }));
		await user.click(screen.getByRole("button", { name: "Check again" }));
		expect(mocks.check).toHaveBeenCalledWith(
			{ projectId: "p1", kind: "orchestrator" },
			{ blockers: ["agent_not_installed"], method: "install" },
		);
		expect(resolve).toHaveBeenCalledWith({ harness: "claude-code" });
		expect(useSpawnPreflightStore.getState().pending).toBeNull();
	});

	it("keeps the dialog open with the new report when a re-check is still blocked", async () => {
		const user = userEvent.setup();
		const stillBlocked: SpawnPreflightReport = {
			ready: false, canProceed: false, kind: "orchestrator", harness: "codex",
			checks: [{ code: "default_branch_unresolved", blocking: true }],
		};
		mocks.check.mockResolvedValue(stillBlocked);
		const { resolve } = openDialog({
			ready: false, canProceed: false, kind: "orchestrator", harness: "codex",
			checks: [{ code: "agent_not_installed", blocking: true }],
		});
		await user.click(screen.getByRole("button", { name: "Check again" }));
		expect(resolve).not.toHaveBeenCalled();
		expect(useSpawnPreflightStore.getState().pending?.report).toEqual(stillBlocked);
		expect(mocks.check.mock.calls[0]?.[1]).toEqual({ blockers: ["agent_not_installed"], method: "recheck" });
	});

	it("switches the orchestrator to another ready agent and persists it", async () => {
		const user = userEvent.setup();
		mocks.check.mockResolvedValue(readyReport);
		const { resolve } = openDialog({
			ready: false, canProceed: false, kind: "orchestrator", harness: "codex",
			checks: [{ code: "agent_not_installed", blocking: true }],
		});
		await user.click(screen.getByRole("combobox", { name: "Use another installed agent" }));
		// Only agents that are ready and not the blocked one are offered.
		expect(screen.queryByRole("option", { name: "opencode" })).toBeNull();
		await user.click(screen.getByRole("option", { name: "Claude Code" }));
		await user.click(screen.getByRole("button", { name: "Use this agent" }));
		expect(mocks.setAgent).toHaveBeenCalledWith("p1", "claude-code");
		expect(mocks.check).toHaveBeenCalledWith(
			{ projectId: "p1", kind: "orchestrator", harness: undefined },
			{ blockers: ["agent_not_installed"], method: "switch_agent" },
		);
		expect(resolve).toHaveBeenCalledWith({ harness: "claude-code" });
	});

	it("switches a worker's harness without changing project config", async () => {
		const user = userEvent.setup();
		mocks.check.mockResolvedValue({ ...readyReport, kind: "worker" });
		const { resolve } = openDialog(
			{ ready: false, canProceed: false, kind: "worker", harness: "codex", checks: [{ code: "agent_unauthorized", blocking: true }] },
			{ projectId: "p1", kind: "worker", harness: "codex" },
		);
		expect(screen.getByRole("dialog", { name: "Finish setup to start this session" })).toBeInTheDocument();
		await user.click(screen.getByRole("combobox", { name: "Use another installed agent" }));
		await user.click(screen.getByRole("option", { name: "Claude Code" }));
		await user.click(screen.getByRole("button", { name: "Use this agent" }));
		expect(mocks.setAgent).not.toHaveBeenCalled();
		expect(resolve).toHaveBeenCalledWith({ harness: "claude-code" });
	});

	it("saves a default branch, then re-checks", async () => {
		const user = userEvent.setup();
		mocks.check.mockResolvedValue(readyReport);
		openDialog({
			ready: false, canProceed: false, kind: "orchestrator", harness: "claude-code",
			checks: [{ code: "default_branch_unresolved", blocking: true }],
		});
		expect(screen.getByText("The default branch could not be found")).toBeInTheDocument();
		await user.type(screen.getByRole("textbox", { name: "Default branch" }), "trunk");
		await user.click(screen.getByRole("button", { name: "Save branch" }));
		expect(mocks.setBranch).toHaveBeenCalledWith("p1", "trunk");
		expect(mocks.check.mock.calls[0]?.[1]).toEqual({ blockers: ["default_branch_unresolved"], method: "set_default_branch" });
	});

	it("shows a save failure next to the actions and does not re-check", async () => {
		const user = userEvent.setup();
		mocks.setBranch.mockRejectedValue(new Error("Could not save the project"));
		openDialog({
			ready: false, canProceed: false, kind: "orchestrator", harness: "claude-code",
			checks: [{ code: "default_branch_unresolved", blocking: true }],
		});
		await user.type(screen.getByRole("textbox", { name: "Default branch" }), "trunk");
		await user.click(screen.getByRole("button", { name: "Save branch" }));
		expect(screen.getByRole("alert")).toHaveTextContent("Could not save the project");
		expect(mocks.check).not.toHaveBeenCalled();
	});

	it("offers Start anyway only when every failed check is soft", async () => {
		const user = userEvent.setup();
		const { resolve } = openDialog({
			ready: false, canProceed: true, kind: "orchestrator", harness: "claude-code",
			checks: [{ code: "agent_unauthorized", blocking: false, command: "claude /login" }],
		});
		await user.click(screen.getByRole("button", { name: "Start anyway" }));
		expect(resolve).toHaveBeenCalledWith({ harness: "claude-code" });
	});

	it("has no Start anyway for a blocking check and cancels on close", async () => {
		const user = userEvent.setup();
		const { reject, resolve } = openDialog({
			ready: false, canProceed: false, kind: "orchestrator", harness: "codex",
			checks: [{ code: "agent_not_installed", blocking: true }],
		});
		expect(screen.queryByRole("button", { name: "Start anyway" })).toBeNull();
		await user.keyboard("{Escape}");
		expect(resolve).not.toHaveBeenCalled();
		expect(reject).toHaveBeenCalledOnce();
		expect(screen.queryByRole("dialog")).toBeNull();
	});

	it("installs with one click and re-checks when the job succeeds", async () => {
		const user = userEvent.setup();
		mocks.install.mockResolvedValue({ target: "codex", status: "succeeded" });
		mocks.check.mockResolvedValue(readyReport);
		const { resolve } = openDialog({
			ready: false, canProceed: false, kind: "orchestrator", harness: "codex",
			checks: [{ code: "agent_not_installed", blocking: true, command: "npm install -g @openai/codex", canInstall: true }],
		});
		await user.click(screen.getByRole("button", { name: "Install" }));
		expect(mocks.install).toHaveBeenCalledWith(expect.objectContaining({ code: "agent_not_installed" }), "codex");
		expect(mocks.check.mock.calls[0]?.[1]).toEqual({ blockers: ["agent_not_installed"], method: "install" });
		expect(resolve).toHaveBeenCalledWith({ harness: "claude-code" });
	});

	it("keeps the dialog open with the job error when an install fails", async () => {
		const user = userEvent.setup();
		mocks.install.mockResolvedValue({ target: "tmux", status: "failed", error: "Homebrew is not installed" });
		openDialog({
			ready: false, canProceed: false, kind: "worker", harness: "claude-code",
			checks: [{ code: "runtime_prerequisite_missing", blocking: true, command: "brew install tmux", canInstall: true }],
		});
		await user.click(screen.getByRole("button", { name: "Install" }));
		expect(screen.getByRole("alert")).toHaveTextContent("Homebrew is not installed");
		expect(mocks.check).not.toHaveBeenCalled();
	});

	it("signs in through the embedded login terminal and re-checks when it exits", async () => {
		const user = userEvent.setup();
		mocks.startAuth.mockResolvedValue({
			action: "login",
			terminal: { handleId: "shell-1", title: "Sign in to Codex", workingDir: "/tmp", createdAt: "2026-10-28T00:00:00Z" },
		});
		mocks.check.mockResolvedValue(readyReport);
		const { resolve } = openDialog({
			ready: false, canProceed: false, kind: "orchestrator", harness: "codex",
			checks: [{ code: "agent_unauthorized", blocking: true, command: "codex login", canStartLogin: true }],
		});
		await user.click(screen.getByRole("button", { name: "Sign in" }));
		expect(mocks.startAuth).toHaveBeenCalledWith("codex");
		expect(screen.getByText("Sign in to Codex")).toBeInTheDocument();
		expect(mocks.check).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Exit login" }));
		expect(mocks.closeTerminal).toHaveBeenCalledWith("shell-1");
		expect(mocks.check.mock.calls[0]?.[1]).toEqual({ blockers: ["agent_unauthorized"], method: "login" });
		expect(resolve).toHaveBeenCalledWith({ harness: "claude-code" });
	});

	it("closes an open login terminal when the dialog is dismissed", async () => {
		const user = userEvent.setup();
		mocks.startAuth.mockResolvedValue({
			action: "login",
			terminal: { handleId: "shell-2", title: "Sign in to Codex", workingDir: "/tmp", createdAt: "2026-10-28T00:00:00Z" },
		});
		openDialog({
			ready: false, canProceed: false, kind: "orchestrator", harness: "codex",
			checks: [{ code: "agent_unauthorized", blocking: true, canStartLogin: true }],
		});
		await user.click(screen.getByRole("button", { name: "Sign in" }));
		await user.keyboard("{Escape}");
		expect(mocks.closeTerminal).toHaveBeenCalledWith("shell-2");
	});

	describe("a late result from a replaced launch", () => {
		const blockedA: SpawnPreflightReport = {
			ready: false, canProceed: false, kind: "orchestrator", harness: "codex",
			checks: [{ code: "agent_not_installed", blocking: true, canInstall: true, canStartLogin: true }],
		};
		const blockedB: SpawnPreflightReport = {
			ready: false, canProceed: false, kind: "orchestrator", harness: "claude-code",
			checks: [{ code: "default_branch_unresolved", blocking: true }],
		};

		function deferred<T>() {
			let resolve!: (value: T) => void;
			const promise = new Promise<T>((done) => { resolve = done; });
			return { promise, resolve };
		}

		function openB() {
			const b = { resolve: vi.fn(), reject: vi.fn() };
			act(() => {
				useSpawnPreflightStore.getState().open({ input: { projectId: "proj-b", kind: "orchestrator" }, report: blockedB, ...b });
			});
			return b;
		}

		function expectBUntouched(b: { resolve: ReturnType<typeof vi.fn>; reject: ReturnType<typeof vi.fn> }) {
			expect(b.resolve).not.toHaveBeenCalled();
			expect(b.reject).not.toHaveBeenCalled();
			expect(useSpawnPreflightStore.getState().pending).toMatchObject({ report: blockedB, input: { projectId: "proj-b" } });
			expect(screen.getByText("The default branch could not be found")).toBeInTheDocument();
		}

		it("does not start or overwrite B when A's re-check finishes after B opened", async () => {
			const user = userEvent.setup();
			const check = deferred<SpawnPreflightReport>();
			mocks.check.mockReturnValueOnce(check.promise);
			const a = openDialog(blockedA, { projectId: "proj-a", kind: "orchestrator" });
			await user.click(screen.getByRole("button", { name: "Check again" }));
			const b = openB();
			expect(a.reject).toHaveBeenCalledOnce();
			// A ready result would have spawned B with A's harness.
			await act(async () => check.resolve(readyReport));
			expectBUntouched(b);
			expect(a.resolve).not.toHaveBeenCalled();
		});

		it("keeps B's report when A's still-blocked re-check finishes after B opened", async () => {
			const user = userEvent.setup();
			const check = deferred<SpawnPreflightReport>();
			mocks.check.mockReturnValueOnce(check.promise);
			openDialog(blockedA, { projectId: "proj-a", kind: "orchestrator" });
			await user.click(screen.getByRole("button", { name: "Check again" }));
			const b = openB();
			await act(async () => check.resolve(blockedA));
			expectBUntouched(b);
		});

		it("does not re-check for B when A's install finishes after B opened", async () => {
			const user = userEvent.setup();
			const job = deferred<{ target: string; status: string }>();
			mocks.install.mockReturnValueOnce(job.promise);
			mocks.check.mockResolvedValue(readyReport);
			openDialog(blockedA, { projectId: "proj-a", kind: "orchestrator" });
			await user.click(screen.getByRole("button", { name: "Install" }));
			const b = openB();
			await act(async () => job.resolve({ target: "codex", status: "succeeded" }));
			expect(mocks.check).not.toHaveBeenCalled();
			expectBUntouched(b);
		});

		it("closes A's login terminal and leaves B alone when sign-in starts after B opened", async () => {
			const user = userEvent.setup();
			const auth = deferred<unknown>();
			mocks.startAuth.mockReturnValueOnce(auth.promise);
			openDialog({ ...blockedA, checks: [{ code: "agent_unauthorized", blocking: true, canStartLogin: true }] }, { projectId: "proj-a", kind: "orchestrator" });
			await user.click(screen.getByRole("button", { name: "Sign in" }));
			const b = openB();
			await act(async () => auth.resolve({
				action: "login",
				terminal: { handleId: "shell-a", title: "Sign in to Codex", workingDir: "/tmp", createdAt: "2026-10-28T00:00:00Z" },
			}));
			expect(mocks.closeTerminal).toHaveBeenCalledWith("shell-a");
			expect(screen.queryByText("Sign in to Codex")).toBeNull();
			expect(mocks.check).not.toHaveBeenCalled();
			expectBUntouched(b);
		});
	});
});
