import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import { McpServerBanner, ReauthBanner, ThreadStateBanner, VersionSwitchBanner } from "./ChatStatusBanners";

const versionRefreshSetting = vi.hoisted(() => ({ enabled: true }));
vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { versionRefresh: versionRefreshSetting.enabled }, isLoading: false, error: undefined }),
}));
vi.mock("../../lib/host-clients", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../lib/host-clients")>()),
	labelForHost: (hostId: string) => (hostId === "host-air" ? "Macbook Air M1" : undefined),
}));

// Each of these answers a question the timeline structurally cannot, so the tests are
// about what is said and when it is withheld — a banner for an ordinary state is noise
// that teaches readers to ignore the row.

describe("ReauthBanner", () => {
	const failed = { authenticationState: "required" as const, authFailureId: "f1", reauthReason: "OAuth token has been revoked" };
	const openGlobalSettings = vi.fn();
	const original = useUiStore.getState().openGlobalSettings;
	beforeEach(() => useUiStore.setState({ openGlobalSettings }));
	afterEach(() => {
		useUiStore.setState({ openGlobalSettings: original });
		openGlobalSettings.mockReset();
	});

	it("says the login expired in plain words, without the provider's error", () => {
		render(<ReauthBanner account={failed} harness="claude-code" />);
		expect(screen.getByRole("status")).toHaveTextContent("Your Claude Code login has expired. Log in again to keep using this chat.");
		expect(screen.queryByText(/revoked/)).not.toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("opens the Harness page, which starts the login", async () => {
		render(<ReauthBanner account={{ reauthRequiredAt: "2026-08-03T00:00:00Z" }} harness="codex" />);
		await userEvent.click(screen.getByRole("button", { name: "Log in again" }));
		expect(openGlobalSettings).toHaveBeenCalledWith("harness", {
			focusAgentId: "codex", harnessView: "local", startLogin: true, preserveProject: true,
		});
	});

	// A remote session runs on its host, so that host's login is the one to fix.
	it("names a remote host and logs in on it", async () => {
		render(<ReauthBanner account={failed} harness="claude-code" hostId="host-air" />);
		expect(screen.getByRole("status")).toHaveTextContent("Your Claude Code login on Macbook Air M1 has expired.");
		await userEvent.click(screen.getByRole("button", { name: "Log in again" }));
		expect(openGlobalSettings).toHaveBeenCalledWith("harness", {
			focusAgentId: "claude-code", hostId: "host-air", harnessView: "local", startLogin: true, preserveProject: true,
		});
	});

	it("logs a cloud session in on the cloud connection", async () => {
		render(<ReauthBanner account={failed} harness="codex" cloud />);
		await userEvent.click(screen.getByRole("button", { name: "Log in again" }));
		expect(openGlobalSettings).toHaveBeenCalledWith("harness", expect.objectContaining({ focusAgentId: "codex", harnessView: "cloud" }));
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
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
		rerender(<ReauthBanner account={{ ...account, planLabel: "Pro" }} harness="codex" />);
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
		rerender(<ReauthBanner account={{ ...account, authFailureId: "second" }} harness="codex" />);
		expect(screen.getByRole("status")).toBeInTheDocument();
	});

	it("uses current authentication state even when historical failure evidence exists", () => {
		render(<ReauthBanner account={{ authenticationState: "authenticated", reauthRequiredAt: "2026-08-03T00:00:00Z", lastAuthFailureReason: "expired" }} harness="codex" />);
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
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
