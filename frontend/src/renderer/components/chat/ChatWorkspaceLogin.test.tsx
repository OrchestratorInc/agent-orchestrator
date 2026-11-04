/**
 * A turn the provider refused for a missing or expired login.
 *
 * The provider's advice ("Please run /login") is for its own CLI and means
 * nothing in Chat, so the turn offers the login instead: the Agents page opens
 * on it, and a login that takes there brings the user back and, when the turn
 * did no work, runs it again.
 */

import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationSnapshot } from "../../types/conversation";
import { renderChat as render } from "../../test/render-chat";
import { useUiStore } from "../../stores/ui-store";

const CLI_ADVICE = "Not logged in · Please run /login";

/** turn-1 failed on a refused login. With `didWork` it also ran commands and changed files first. */
function signedOutSnapshot({ didWork }: { didWork: boolean }): ConversationSnapshot {
	const failed = {
		...chatFixture.turns[0]!,
		state: "failed" as const,
		completedAt: chatFixture.turns[0]!.requestedAt,
		errorMessage: CLI_ADVICE,
		failureCode: "auth_required" as const,
		...(didWork ? {} : { diff: undefined }),
	};
	return {
		...chatFixture,
		harness: "claude-code",
		controller: { state: "ready" },
		turns: [failed],
		items: chatFixture.items.filter((item) => item.turnId === "turn-1" &&
			(didWork || (item.kind === "message" && item.role === "user"))),
		account: { authenticationState: "required", reauthRequiredAt: failed.completedAt, reauthReason: CLI_ADVICE },
	};
}

afterEach(() => {
	act(() => useUiStore.setState({ settingsModal: null, loginReturn: null }));
});

describe("ChatWorkspace refused login", () => {
	it("offers a login in place of the provider's CLI advice, and only once", () => {
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry: vi.fn() }} />);

		const box = screen.getByTestId("login-notice");
		expect(within(box).getByText("You’re not logged in to Claude Code")).toBeInTheDocument();
		expect(within(box).getByText("This turn runs again once you’re in.")).toBeInTheDocument();
		expect(within(box).getByRole("button", { name: "Log in" })).toBeInTheDocument();
		// The CLI's advice means nothing here, so it is not shown at all.
		expect(screen.queryByText(CLI_ADVICE)).toBeNull();
		expect(screen.queryByText("Technical details")).toBeNull();
		expect(screen.queryByText("The agent ran into a problem")).toBeNull();
		// The card already offers the login, so the banner does not repeat it.
		expect(screen.queryByRole("button", { name: "Log in again" })).toBeNull();
		expect(screen.getByRole("button", { name: "Retry this turn" })).toBeInTheDocument();
	});

	it("opens the Agents page on the login, then returns and runs a turn that did no work again", async () => {
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);

		await userEvent.click(screen.getByRole("button", { name: "Log in" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({
			scope: "global", section: "harness", focusAgentId: "claude-code", harnessView: "local", startLogin: true,
		});
		expect(retry).not.toHaveBeenCalled();

		act(() => useUiStore.getState().completeLoginReturn("claude-code"));

		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(retry).toHaveBeenCalledOnce();
		expect(retry).toHaveBeenCalledWith("turn-1");
		expect(useUiStore.getState().loginReturn).toBeNull();
	});

	it("returns without running a turn again that already ran commands", async () => {
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: true })} retryControl={{ retry }} />);
		expect(screen.getByText("Retry this turn once you’re in.")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Log in" }));
		act(() => useUiStore.getState().completeLoginReturn("claude-code"));

		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(retry).not.toHaveBeenCalled();
		// Retry stays the user's call.
		await userEvent.click(screen.getByRole("button", { name: "Retry this turn" }));
		expect(retry).toHaveBeenCalledWith("turn-1");
	});

	it("waits for the login on the session's own host", async () => {
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} remoteHostId="host-a" retryControl={{ retry }} />);
		expect(screen.getByText("You’re not logged in to Claude Code on host-a")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Log in" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ hostId: "host-a", startLogin: true });

		// A login on this computer, or for another agent, does not fix that host's.
		act(() => useUiStore.getState().completeLoginReturn("claude-code"));
		act(() => useUiStore.getState().completeLoginReturn("codex", "host-a"));
		expect(useUiStore.getState().settingsModal).not.toBeNull();
		expect(retry).not.toHaveBeenCalled();

		act(() => useUiStore.getState().completeLoginReturn("claude-code", "host-a"));
		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(retry).toHaveBeenCalledWith("turn-1");
	});

	it("keeps the banner when the latest turn did not fail on the login", () => {
		const snapshot = signedOutSnapshot({ didWork: false });
		snapshot.turns = [{ ...snapshot.turns[0]!, failureCode: undefined, errorMessage: "stream disconnected" }];
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		expect(screen.getByRole("button", { name: "Log in again" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Log in" })).toBeNull();
		expect(screen.getByText("The agent ran into a problem")).toBeInTheDocument();
	});
});
