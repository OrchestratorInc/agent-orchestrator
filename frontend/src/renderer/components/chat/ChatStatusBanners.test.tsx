import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { McpServerBanner, ReauthBanner, ThreadStateBanner } from "./ChatStatusBanners";

// Each of these answers a question the timeline structurally cannot, so the tests are
// about what is said and when it is withheld — a banner for an ordinary state is noise
// that teaches readers to ignore the row.

describe("ReauthBanner", () => {
	it.each(["Unauthorized (401)", "Authentication failed", "OAuth token has been revoked"])(
		"offers sign-in based on account state for %s",
		(reason) => {
			render(<ReauthBanner account={{ reauthRequiredAt: "2026-09-14T00:00:00Z", reauthReason: reason }} harness="claude-code" />);
			expect(screen.getByRole("alert")).toHaveTextContent(reason);
			expect(screen.getByText("claude auth login")).toBeInTheDocument();
		},
	);
	it("names the command, because re-authenticating is not something AO can do", () => {
		render(
			<ReauthBanner
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
			<ReauthBanner account={{ reauthRequiredAt: "2026-08-03T00:00:00Z" }} harness="codex" />,
		);
		expect(screen.queryByText(/worktree is untouched|Nothing will run/i)).not.toBeInTheDocument();
	});

	it("names Claude Code's non-interactive authentication command", () => {
		render(
			<ReauthBanner account={{ reauthRequiredAt: "2026-08-03T00:00:00Z" }} harness="claude-code" />,
		);
		expect(screen.getByText("claude auth login")).toBeInTheDocument();
	});

	it("falls back to generic wording rather than guessing a command", () => {
		render(
			<ReauthBanner account={{ reauthRequiredAt: "2026-08-03T00:00:00Z" }} harness="opencode" />,
		);
		expect(screen.queryByText(/login$/)).not.toBeInTheDocument();
		expect(screen.getByText(/agent’s own CLI/)).toBeInTheDocument();
	});

	it("stays silent for an account with no credential demand", () => {
		const { container } = render(
			<ReauthBanner account={{ authMode: "chatgpt", planLabel: "Pro" }} harness="codex" />,
		);
		expect(container).toBeEmptyDOMElement();
	});
	it("dismisses only this failure and displays a new failure", async () => {
		const account = { authenticationState: "required" as const, authFailureId: "first", reauthRequiredAt: "2026-08-03T00:00:00Z" };
		const { rerender } = render(<ReauthBanner account={account} harness="codex" />);
		await userEvent.click(screen.getByRole("button", { name: "Dismiss authentication notice" }));
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		rerender(<ReauthBanner account={{ ...account, planLabel: "Pro" }} harness="codex" />);
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		rerender(<ReauthBanner account={{ ...account, authFailureId: "second" }} harness="codex" />);
		expect(screen.getByRole("alert")).toBeInTheDocument();
	});

	it("uses current authentication state even when historical failure evidence exists", () => {
		render(<ReauthBanner account={{ authenticationState: "authenticated", reauthRequiredAt: "2026-08-03T00:00:00Z", lastAuthFailureReason: "expired" }} harness="codex" />);
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
