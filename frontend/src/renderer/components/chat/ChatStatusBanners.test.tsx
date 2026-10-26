import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { McpServerBanner, ReauthBanner, ThreadStateBanner, VersionSwitchBanner } from "./ChatStatusBanners";

const versionRefreshSetting = vi.hoisted(() => ({ enabled: true }));
vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { versionRefresh: versionRefreshSetting.enabled }, isLoading: false, error: undefined }),
}));

type MockAccount = { id: string; provider: "claude" | "codex"; device: boolean; email?: string; signedIn: boolean; default: boolean };
const accountsMock = vi.hoisted(() => ({
	session: {} as Record<string, string>,
	accounts: [] as MockAccount[],
	actions: {
		signInAgain: vi.fn(),
		verifyLogin: vi.fn(),
		cancelLogin: vi.fn(),
		switchSession: vi.fn(),
		refreshSession: vi.fn(),
	},
}));
vi.mock("../../hooks/useAgentAccounts", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../hooks/useAgentAccounts")>()),
	useSessionAgentAccounts: (sessionId: string | undefined) => ({ data: sessionId ? accountsMock.session : undefined }),
	useAgentAccounts: (enabled = true) => ({ data: enabled ? accountsMock.accounts : undefined }),
	useAgentAccountActions: () => accountsMock.actions,
}));
// The real dialog hosts a terminal; these tests only need its check and close.
vi.mock("../settings/AgentAccountsSection", () => ({
	AgentAccountLoginDialog: ({ login, onCheck, onClose }: { login: unknown; onCheck: (login: unknown) => void; onClose: (login: unknown) => void }) => (
		<div data-testid="login-dialog">
			<button type="button" onClick={() => onCheck(login)}>check sign-in</button>
			<button type="button" onClick={() => onClose(login)}>close sign-in</button>
		</div>
	),
}));

afterEach(() => {
	accountsMock.session = {};
	accountsMock.accounts = [];
	for (const fn of Object.values(accountsMock.actions)) fn.mockReset();
});

// Each of these answers a question the timeline structurally cannot, so the tests are
// about what is said and when it is withheld — a banner for an ordinary state is noise
// that teaches readers to ignore the row.

describe("ReauthBanner", () => {
	it.each(["Unauthorized (401)", "Authentication failed", "OAuth token has been revoked"])(
		"offers sign-in based on account state for %s",
		(reason) => {
			render(<ReauthBanner sessionId="s1" account={{ reauthRequiredAt: "2026-09-14T00:00:00Z", reauthReason: reason }} harness="claude-code" />);
			expect(screen.getByRole("alert")).toHaveTextContent(reason);
			expect(screen.getByText("claude auth login")).toBeInTheDocument();
		},
	);
	it("names the command, because re-authenticating is not something AO can do", () => {
		render(
			<ReauthBanner sessionId="s1"
				account={{
					reauthRequiredAt: "2026-08-03T00:00:00Z",
					reauthReason: "The stored session expired.",
				}}
				harness="codex"
			/>,
		);
		expect(screen.getByRole("alert")).toBeInTheDocument();
		expect(screen.getByText("codex login")).toBeInTheDocument();
		expect(screen.getByText(/The stored session expired/)).toBeInTheDocument();
	});

	it("does not claim that failed work had no side effects", () => {
		render(
			<ReauthBanner sessionId="s1" account={{ reauthRequiredAt: "2026-08-03T00:00:00Z" }} harness="codex" />,
		);
		expect(screen.queryByText(/worktree is untouched|Nothing will run/i)).not.toBeInTheDocument();
	});

	it("names Claude Code's non-interactive authentication command", () => {
		render(
			<ReauthBanner sessionId="s1" account={{ reauthRequiredAt: "2026-08-03T00:00:00Z" }} harness="claude-code" />,
		);
		expect(screen.getByText("claude auth login")).toBeInTheDocument();
	});

	it("falls back to generic wording rather than guessing a command", () => {
		render(
			<ReauthBanner sessionId="s1" account={{ reauthRequiredAt: "2026-08-03T00:00:00Z" }} harness="opencode" />,
		);
		expect(screen.queryByText(/login$/)).not.toBeInTheDocument();
		expect(screen.getByText(/agent’s own CLI/)).toBeInTheDocument();
	});

	it("stays silent for an account with no credential demand", () => {
		const { container } = render(
			<ReauthBanner sessionId="s1" account={{ authMode: "chatgpt", planLabel: "Pro" }} harness="codex" />,
		);
		expect(container).toBeEmptyDOMElement();
	});
	it("dismisses only this failure and displays a new failure", async () => {
		const account = { authenticationState: "required" as const, authFailureId: "first", reauthRequiredAt: "2026-08-03T00:00:00Z" };
		const { rerender } = render(<ReauthBanner sessionId="s1" account={account} harness="codex" />);
		await userEvent.click(screen.getByRole("button", { name: "Dismiss authentication notice" }));
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		rerender(<ReauthBanner sessionId="s1" account={{ ...account, planLabel: "Pro" }} harness="codex" />);
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		rerender(<ReauthBanner sessionId="s1" account={{ ...account, authFailureId: "second" }} harness="codex" />);
		expect(screen.getByRole("alert")).toBeInTheDocument();
	});

	describe("on a saved account", () => {
		const team: MockAccount = { id: "team", provider: "claude", device: false, email: "team@example.com", signedIn: false, default: true };
		const personal: MockAccount = { id: "device-claude", provider: "claude", device: true, email: "me@example.com", signedIn: true, default: false };
		const failed = { authenticationState: "required" as const, authFailureId: "f1", reauthReason: "Failed to authenticate: OAuth session expired and could not be refreshed" };

		it("names the signed-out account and signs it in again in its own folder", async () => {
			accountsMock.session = { claude: "team" };
			accountsMock.accounts = [team];
			accountsMock.actions.signInAgain.mockResolvedValue({ operationId: "op1", provider: "claude" });
			accountsMock.actions.verifyLogin.mockResolvedValue({ status: "complete" });
			render(<ReauthBanner sessionId="s1" account={failed} harness="claude-code" />);
			expect(screen.getByRole("alert")).toHaveTextContent("team@example.com is signed out");
			// The computer's own sign-in command would not fix this account's folder.
			expect(screen.queryByText("claude auth login")).not.toBeInTheDocument();
			expect(screen.queryByRole("button", { name: /Continue on/ })).not.toBeInTheDocument();
			await userEvent.click(screen.getByRole("button", { name: "Sign in again" }));
			expect(accountsMock.actions.signInAgain).toHaveBeenCalledWith("team");
			await userEvent.click(screen.getByRole("button", { name: "check sign-in" }));
			expect(accountsMock.actions.verifyLogin).toHaveBeenCalledWith("op1");
			expect(screen.queryByTestId("login-dialog")).not.toBeInTheDocument();
			expect(screen.getByRole("status")).toHaveTextContent("Signed in again. Retry your last message to continue.");
		});

		it("keeps the sign-in open while it is not finished, and cancels it on close", async () => {
			accountsMock.session = { claude: "team" };
			accountsMock.accounts = [team];
			accountsMock.actions.signInAgain.mockResolvedValue({ operationId: "op1", provider: "claude" });
			accountsMock.actions.verifyLogin.mockResolvedValue({ status: "waiting" });
			accountsMock.actions.cancelLogin.mockResolvedValue(undefined);
			render(<ReauthBanner sessionId="s1" account={failed} harness="claude-code" />);
			await userEvent.click(screen.getByRole("button", { name: "Sign in again" }));
			await userEvent.click(screen.getByRole("button", { name: "check sign-in" }));
			expect(screen.getByTestId("login-dialog")).toBeInTheDocument();
			await userEvent.click(screen.getByRole("button", { name: "close sign-in" }));
			expect(accountsMock.actions.cancelLogin).toHaveBeenCalledWith("op1");
			expect(screen.queryByTestId("login-dialog")).not.toBeInTheDocument();
		});

		it("moves the chat to another signed-in account", async () => {
			accountsMock.session = { claude: "team" };
			accountsMock.accounts = [personal, team];
			accountsMock.actions.switchSession.mockResolvedValue({});
			accountsMock.actions.refreshSession.mockResolvedValue(undefined);
			render(<ReauthBanner sessionId="s1" account={failed} harness="claude-code" />);
			await userEvent.click(screen.getByRole("button", { name: "Continue on me@example.com" }));
			expect(accountsMock.actions.switchSession).toHaveBeenCalledWith("s1", "device-claude");
			expect(screen.getByRole("status")).toHaveTextContent("Moving this chat to me@example.com.");
		});

		it("offers another account to a chat on the computer's own login too", () => {
			accountsMock.session = { claude: "device-claude" };
			accountsMock.accounts = [{ ...personal, signedIn: false }, { ...team, signedIn: true }];
			render(<ReauthBanner sessionId="s1" account={failed} harness="claude-code" />);
			expect(screen.getByText("claude auth login")).toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Sign in again" })).not.toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Continue on team@example.com" })).toBeInTheDocument();
		});
	});

	it("uses current authentication state even when historical failure evidence exists", () => {
		render(<ReauthBanner sessionId="s1" account={{ authenticationState: "authenticated", reauthRequiredAt: "2026-08-03T00:00:00Z", lastAuthFailureReason: "expired" }} harness="codex" />);
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

});

describe("ThreadStateBanner", () => {
	it("reports a provider-side fault as the provider's, not AO's connection", () => {
		render(<ThreadStateBanner threadState={{ status: "system_error" }} />);
		expect(screen.getByText(/thread hit an internal error/i)).toBeInTheDocument();
		expect(screen.getByText(/not in AO's connection to it/)).toBeInTheDocument();
	});

	it("reports a closed thread as history AO kept and the agent did not", () => {
		render(<ThreadStateBanner threadState={{ status: "closed" }} />);
		expect(screen.getByText(/closed this thread/i)).toBeInTheDocument();
	});

	it("lists what the provider says it is waiting on", () => {
		render(
			<ThreadStateBanner threadState={{ status: "system_error", waitingOn: ["user_input"] }} />,
		);
		expect(screen.getByText(/Waiting on: user_input/)).toBeInTheDocument();
	});

	// active, idle and not_loaded are the ordinary run of a session.
	it.each(["active", "idle", "not_loaded"] as const)("says nothing for %s", (status) => {
		const { container } = render(<ThreadStateBanner threadState={{ status }} />);
		expect(container).toBeEmptyDOMElement();
	});
});

describe("McpServerBanner", () => {
	afterEach(() => {
		vi.useRealTimers();
		window.localStorage.clear();
	});

	const broken = [
		{
			name: "playwright",
			status: "failed" as const,
			failureReason: "startup_timeout",
			error: "did not report ready within 30s",
		},
	];

	it("names the missing tools in one quiet line, without diagnostics or controls", () => {
		render(<McpServerBanner sessionId="mcp-line" servers={broken} />);
		expect(screen.getByRole("status")).toHaveTextContent("Playwright didn’t start. Continuing without it.");
		expect(screen.queryByText(/startup_timeout|did not report ready/)).not.toBeInTheDocument();
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
		// It overlays the end of the timeline for a few seconds; clicks must pass through.
		expect(screen.getByRole("status")).toHaveClass("pointer-events-none");
	});

	it("lists several servers together", () => {
		render(<McpServerBanner sessionId="mcp-several" servers={[...broken, { name: "notion", status: "failed" }]} />);
		expect(screen.getByRole("status")).toHaveTextContent("Playwright, Notion didn’t start. Continuing without them.");
	});

	it("goes away on its own after a few seconds", () => {
		vi.useFakeTimers();
		render(<McpServerBanner sessionId="mcp-timeout" servers={broken} />);
		expect(screen.getByRole("status")).toBeInTheDocument();
		act(() => vi.advanceTimersByTime(3_000));
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
		act(() => vi.advanceTimersByTime(200));
		expect(screen.queryByText("Playwright didn’t start. Continuing without it.")).not.toBeInTheDocument();
	});

	it("shows once per session, even when the chat remounts", () => {
		const first = render(<McpServerBanner sessionId="mcp-once" servers={broken} />);
		expect(screen.getByText("Playwright didn’t start. Continuing without it.")).toBeInTheDocument();
		first.unmount();

		render(<McpServerBanner sessionId="mcp-once" servers={broken} />);
		expect(screen.queryByText("Playwright didn’t start. Continuing without it.")).not.toBeInTheDocument();
		expect(window.localStorage.getItem("ao:mcp-notice-shown:mcp-once")).toBe("1");
	});

	it("tells a restarted session again", () => {
		const first = render(<McpServerBanner sessionId="mcp-restart" incarnation="run-1" servers={broken} />);
		expect(screen.getByRole("status")).toBeInTheDocument();
		first.unmount();

		render(<McpServerBanner sessionId="mcp-restart" incarnation="run-2" servers={broken} />);
		expect(screen.getByRole("status")).toHaveTextContent("Playwright didn’t start. Continuing without it.");
	});

	it("does not replay for a session that already showed it before a reload", () => {
		window.localStorage.setItem("ao:mcp-notice-shown:mcp-restored", "1");
		const { container } = render(<McpServerBanner sessionId="mcp-restored" servers={broken} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("waits until the chat panel is visible before using up the note", () => {
		const { rerender } = render(<McpServerBanner sessionId="mcp-hidden" servers={broken} active={false} />);
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
		expect(window.localStorage.getItem("ao:mcp-notice-shown:mcp-hidden")).toBeNull();

		rerender(<McpServerBanner sessionId="mcp-hidden" servers={broken} active />);
		expect(screen.getByRole("status")).toHaveTextContent("Playwright didn’t start. Continuing without it.");
	});

	// A healthy server is not news. The caller filters, and an empty list must not
	// draw anything or use up the session's one note.
	it("says nothing when no server is broken", () => {
		const { container, rerender } = render(<McpServerBanner sessionId="mcp-healthy" servers={[]} />);
		expect(container).toBeEmptyDOMElement();
		rerender(<McpServerBanner sessionId="mcp-healthy" servers={broken} />);
		expect(screen.getByRole("status")).toBeInTheDocument();
	});
});

describe("VersionSwitchBanner", () => {
	afterEach(() => { versionRefreshSetting.enabled = true; });

	it("says nothing for a session on the installed release", () => {
		const { container } = render(<VersionSwitchBanner />);
		expect(container).toBeEmptyDOMElement();
	});

	it("tells the user the session moves to the new release when idle", () => {
		render(<VersionSwitchBanner staleBinary={{ version: "2.1.300", since: "2026-10-15T00:00:00Z" }} />);
		expect(screen.getByRole("status")).toHaveTextContent("Switching to v2.1.300…");
	});

	it("asks for a resume instead of promising a switch when the agent stopped", () => {
		render(<VersionSwitchBanner staleBinary={{ version: "2.1.308", since: "2026-10-20T00:00:00Z" }} agentStopped />);
		const status = screen.getByRole("status");
		expect(status).toHaveTextContent("v2.1.308 installed");
		expect(status).toHaveTextContent("Resume the agent to start it on the new version.");
		expect(status).not.toHaveTextContent("Switching");
	});

	it("stays quiet when moving sessions is turned off", () => {
		versionRefreshSetting.enabled = false;
		const { container } = render(<VersionSwitchBanner staleBinary={{ version: "2.1.300", since: "2026-10-15T00:00:00Z" }} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("explains a failed switch and that queued messages were kept", () => {
		versionRefreshSetting.enabled = false;
		render(<VersionSwitchBanner staleBinary={{ version: "2.1.300", since: "2026-10-15T00:00:00Z", switchFailedAt: "2026-10-15T00:10:00Z" }} />);
		expect(screen.getByRole("alert")).toHaveTextContent("Couldn't switch to v2.1.300");
		expect(screen.getByRole("alert")).toHaveTextContent("queued messages are kept");
	});
});
