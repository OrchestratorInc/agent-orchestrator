/**
 * A message the provider refused for a missing or expired login.
 *
 * The provider's advice ("Please run /login") is for its own CLI and means
 * nothing in Chat. The login is offered on a band over the chat box instead,
 * the message keeps one quiet line saying what happens to it, and a login that
 * takes on the Harness page brings the user back and, when the message did no
 * work, sends it again.
 */

import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationSnapshot } from "../../types/conversation";
import type { WorkspaceSession } from "../../types/workspace";
import { renderChat as render } from "../../test/render-chat";
import { useUiStore } from "../../stores/ui-store";
import { apiClient } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";

const CLI_ADVICE = "Not logged in · Please run /login";

/** turn-1 was refused for its login. With `didWork` it also ran commands and changed files first. */
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

const band = () => screen.getByTestId("login-band");
/** The refused messages' lines, in timeline order. */
const statusLines = () => screen.getAllByTestId("login-status");

afterEach(() => {
	act(() => useUiStore.setState({ settingsModal: null, loginReturn: null }));
});

describe("ChatWorkspace refused login", () => {
	it("moves the login onto the chat box and leaves a queued line under the message", () => {
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry: vi.fn() }} />);

		expect(band()).toHaveTextContent("Claude Code is signed out · your message sends once you’re in");
		// Hung on top of the chat box, which keeps its place and fill.
		expect(band().closest(".chat-composer-band-tray")).toBeInTheDocument();
		expect(document.querySelector("[data-composer-tray]")).toBeNull();
		// Nothing new can be sent until the login takes: the agent would refuse it too.
		// Typing is locked, but the box keeps its own look: same placeholder, not dimmed.
		const box = screen.getByLabelText("Message the agent");
		expect(box).toHaveAttribute("aria-readonly", "true");
		expect(box).not.toHaveAttribute("aria-disabled");
		expect(box).toHaveAttribute("aria-placeholder", "Message the agent");
		expect(box).not.toHaveClass("opacity-50");
		expect(within(band()).getByRole("button", { name: "Log in" })).toBeInTheDocument();
		// The status is the message's own: it leads one row under the bubble, with the
		// time and the copy button after it, rather than a line the actions cover.
		const line = screen.getByTestId("login-status");
		expect(line).toHaveTextContent("Queued");
		expect(line.closest("[data-chat-message-id]")).toBeInTheDocument();
		expect(line.closest("[data-turn-outcome]")).toBeNull();
		const row = line.parentElement!.parentElement!;
		expect(row).toContainElement(screen.getByRole("button", { name: "Copy user message" }));
		expect(within(row).getByLabelText(/^Sent /)).toBeInTheDocument();
		// The CLI's advice means nothing here, so it is not shown at all, and the
		// turn has no failure card, no retry row and no banner repeating the login.
		expect(screen.queryByText(CLI_ADVICE)).toBeNull();
		expect(screen.queryByText("The agent ran into a problem")).toBeNull();
		expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Log in again" })).toBeNull();
		// The agent did no work on it, so nothing says it worked for a second.
		expect(document.querySelector("[data-turn-worked]")).toBeNull();
	});

	it("opens the Harness page on the login, then sends the message again once it takes", async () => {
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);

		await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({
			scope: "global", section: "harness", focusAgentId: "claude-code", harnessView: "local", startLogin: true,
		});
		// Back in the chat before the login took, the band says what is pending.
		expect(band()).toHaveTextContent("Finish logging in on the Harness page · your message is waiting");
		expect(within(band()).getByRole("button", { name: "Open again" })).toBeInTheDocument();
		expect(screen.getByTestId("login-status")).toHaveTextContent("Waiting for login");
		expect(retry).not.toHaveBeenCalled();

		act(() => useUiStore.getState().completeLoginReturn("claude-code"));

		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(retry).toHaveBeenCalledOnce();
		expect(retry).toHaveBeenCalledWith("turn-1");
		// Settled rather than cleared, so a send that fails still knows the user is in.
		expect(useUiStore.getState().loginReturn?.settled).toBe(true);
		// In again, so the chat box is open again.
		expect(screen.getByLabelText("Message the agent")).not.toHaveAttribute("aria-readonly");
	});

	it("says it is sending from the moment it sends the message again, with nothing to click twice", async () => {
		// The send is on its way, and the retry has not reported pending yet.
		const retry = vi.fn(() => new Promise<never>(() => {}));
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
		await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));

		act(() => useUiStore.getState().completeLoginReturn("claude-code"));
		await act(async () => {});

		expect(retry).toHaveBeenCalledOnce();
		expect(band()).toHaveAttribute("data-login-state", "sending");
		expect(band()).toHaveTextContent("Logged in to Claude Code · sending your message");
		expect(within(band()).queryByRole("button", { name: "Send again" })).toBeNull();
	});

	it("offers Send again while it waits, for a login finished somewhere this chat does not watch", async () => {
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
		await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));
		act(() => useUiStore.getState().closeSettings());

		// Logged in from a terminal, say: the Harness page never sees it.
		expect(band()).toHaveAttribute("data-login-state", "waiting");
		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));
		expect(retry).toHaveBeenCalledOnce();
		expect(retry).toHaveBeenCalledWith("turn-1");
	});

	it("offers Send again while signed out, for a login fixed before the daemon looks again", async () => {
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);

		expect(band()).toHaveAttribute("data-login-state", "signedOut");
		// Logged in from a terminal, say: no trip through the Harness page to send it.
		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));
		expect(retry).toHaveBeenCalledOnce();
		expect(retry).toHaveBeenCalledWith("turn-1");
		expect(useUiStore.getState().settingsModal).toBeFalsy();
	});

	it("says only that it is sending when Send again goes before any login", async () => {
		const retry = vi.fn();
		const { rerender } = render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));

		rerender(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry, pending: true, turnId: "turn-1" }} />);
		expect(band()).toHaveAttribute("data-login-state", "sending");
		// Nothing has said the user is in: the agent may refuse it again.
		expect(band()).toHaveTextContent("Sending…");
		expect(band()).not.toHaveTextContent("Logged in");
	});

	it("says when Send again failed while signed out, and keeps the login on offer", async () => {
		const retry = vi.fn();
		const { rerender } = render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));

		// Refused before a retry turn existed: no login took, so nothing else would say so.
		rerender(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry, error: "The daemon is not reachable", turnId: "turn-1" }} />);
		expect(band()).toHaveAttribute("data-login-state", "failed");
		expect(band()).toHaveTextContent("Couldn’t send your message · The daemon is not reachable");
		// Still signed out: the login stays the way on, and typing stays locked.
		expect(within(band()).getByRole("button", { name: "Log in" })).toBeInTheDocument();
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute("aria-readonly", "true");
		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));
		expect(retry).toHaveBeenCalledTimes(2);
		expect(retry).toHaveBeenLastCalledWith("turn-1");
	});

	it("opens the chat box once the daemon finds the login working again, however it came back", async () => {
		const retry = vi.fn();
		const snapshot = signedOutSnapshot({ didWork: false });
		const refusedAt = Date.parse(snapshot.turns[0]!.completedAt!);
		// A sign-in the daemon saw before the refusal says nothing about it.
		const before = { ...snapshot, account: { authenticationState: "authenticated" as const, authVerifiedAt: new Date(refusedAt - 60_000).toISOString() } };
		const { rerender } = render(<ChatWorkspace snapshot={before} retryControl={{ retry }} />);
		expect(band()).toHaveAttribute("data-login-state", "signedOut");
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute("aria-readonly", "true");

		const after = { ...snapshot, account: { authenticationState: "authenticated" as const, authVerifiedAt: new Date(refusedAt + 60_000).toISOString() } };
		rerender(<ChatWorkspace snapshot={after} retryControl={{ retry }} />);

		expect(screen.getByLabelText("Message the agent")).not.toHaveAttribute("aria-readonly");
		expect(band()).toHaveTextContent("Logged in to Claude Code · send your message again");
		// Nothing watched this login take, so the message waits for the user's click.
		expect(retry).not.toHaveBeenCalled();
		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));
		expect(retry).toHaveBeenCalledWith("turn-1");
	});

	it("leaves the chat box open when nothing could send the message again", () => {
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} />);

		expect(band()).toBeInTheDocument();
		expect(screen.getByLabelText("Message the agent")).not.toHaveAttribute("aria-readonly");
	});

	it("takes the band away when the chat switches to another agent while it shows", async () => {
		const snapshot = signedOutSnapshot({ didWork: false });
		const { rerender } = render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);
		expect(band()).toBeInTheDocument();

		const prompt = snapshot.items[0]!;
		const switched = {
			...snapshot,
			harness: "codex",
			items: [...snapshot.items, {
				kind: "activity", id: "switch-1", sequence: prompt.sequence + 5, revision: 0, activityKind: "system", status: "completed",
				summary: "Switched to Codex", createdAt: prompt.createdAt, updatedAt: prompt.createdAt,
				detail: { event: "provider_switch", sourceHarness: "claude-code", targetHarness: "codex" },
			} as unknown as typeof prompt],
		};
		rerender(<ChatWorkspace snapshot={switched} retryControl={{ retry: vi.fn() }} />);

		// The chat box opens at once; the band sinks away behind it.
		expect(screen.getByLabelText("Message the agent")).not.toHaveAttribute("aria-readonly");
		await waitFor(() => expect(screen.queryByTestId("login-band")).toBeNull());
	});

	it("lets the chat go on with another agent once it switches away", () => {
		const snapshot = signedOutSnapshot({ didWork: false });
		const prompt = snapshot.items[0]!;
		snapshot.harness = "codex";
		snapshot.items = [...snapshot.items, {
			kind: "activity", id: "switch-1", turnId: undefined, sequence: prompt.sequence + 5, revision: 0, activityKind: "system", status: "completed",
			summary: "Switched to Codex", createdAt: prompt.createdAt, updatedAt: prompt.createdAt,
			detail: { event: "provider_switch", sourceHarness: "claude-code", targetHarness: "codex" },
		} as unknown as typeof prompt];
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		expect(screen.queryByTestId("login-band")).toBeNull();
		expect(screen.getByLabelText("Message the agent")).not.toHaveAttribute("aria-readonly");
		expect(screen.getByTestId("login-status")).toHaveTextContent(/^Stopped$/);
	});

	it("says when sending the message again failed and offers it once more", async () => {
		const retry = vi.fn();
		const { rerender } = render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
		await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));
		act(() => useUiStore.getState().completeLoginReturn("claude-code"));
		expect(retry).toHaveBeenCalledOnce();

		rerender(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry, error: "The daemon is not reachable", turnId: "turn-1" }} />);
		expect(band()).toHaveTextContent("Couldn’t send your message · The daemon is not reachable");
		expect(screen.getByTestId("login-status")).toHaveTextContent(/^Stopped$/);
		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));
		expect(retry).toHaveBeenCalledTimes(2);
		expect(retry).toHaveBeenLastCalledWith("turn-1");
	});

	it("only comes back for the login of the account the session runs on", async () => {
		const get = vi.spyOn(apiClient, "GET").mockImplementation(async (path: string) =>
			(path === "/api/v1/agents/accounts/sessions/{sessionId}" ? { data: { claude: "acct-a" } } : { data: undefined }) as never);
		try {
			const retry = vi.fn();
			render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
			await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));
			// Read while the Harness page opens.
			expect(get).toHaveBeenCalledWith("/api/v1/agents/accounts/sessions/{sessionId}", expect.anything());
			await waitFor(() => expect(useUiStore.getState().loginReturn?.accountId).toBe("acct-a"));

			// Another account signing in leaves this session signed out.
			act(() => useUiStore.getState().completeLoginReturn("claude-code", undefined, "acct-b"));
			expect(useUiStore.getState().settingsModal).not.toBeNull();
			expect(retry).not.toHaveBeenCalled();

			act(() => useUiStore.getState().completeLoginReturn("claude-code", undefined, "acct-a"));
			expect(useUiStore.getState().settingsModal).toBeNull();
			expect(retry).toHaveBeenCalledWith("turn-1");
		} finally {
			get.mockRestore();
		}
	});

	it("signs in the account the session runs on, not this computer's login", async () => {
		const get = vi.spyOn(apiClient, "GET").mockImplementation(async (path: string) =>
			(path === "/api/v1/agents/accounts/sessions/{sessionId}" ? { data: { claude: "acct-a" } } : { data: undefined }) as never);
		try {
			render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry: vi.fn() }} />);
			// Read as soon as the band offers the login, so the click knows it.
			await waitFor(() => expect(get).toHaveBeenCalledWith("/api/v1/agents/accounts/sessions/{sessionId}", expect.anything()));
			await act(async () => undefined);
			await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));

			expect(useUiStore.getState().settingsModal).toMatchObject({ section: "harness", focusAgentId: "claude-code" });
			expect(useUiStore.getState().settingsModal).not.toHaveProperty("startLogin", true);
			expect(useUiStore.getState().accountLogin).toBe("acct-a");
			expect(useUiStore.getState().loginReturn?.accountId).toBe("acct-a");
		} finally {
			get.mockRestore();
			act(() => useUiStore.setState({ accountLogin: null }));
		}
	});

	/** The session's account lookup, answered when the test says so. */
	function slowAccountLookup() {
		let answer: (accounts: Record<string, string>) => void = () => {};
		const get = vi.spyOn(apiClient, "GET").mockImplementation(((path: string) =>
			path === "/api/v1/agents/accounts/sessions/{sessionId}"
				? new Promise((resolve) => {
					answer = (data) => resolve({ data });
				})
				: Promise.resolve({ data: undefined })) as never);
		return { get, answer: (accounts: Record<string, string>) => answer(accounts) };
	}

	it("counts no login for another account while it reads the account the session runs on", async () => {
		const lookup = slowAccountLookup();
		try {
			const retry = vi.fn();
			render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
			await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));
			expect(useUiStore.getState().loginReturn?.accountPending).toBe(true);

			// A login that takes before the account is known could be another account's.
			act(() => useUiStore.getState().completeLoginReturn("claude-code", undefined, "device"));
			expect(useUiStore.getState().loginReturn?.loggedInAt).toBeUndefined();
			expect(useUiStore.getState().settingsModal).not.toBeNull();
			expect(retry).not.toHaveBeenCalled();

			// It was: the session runs on another account, so it stays signed out.
			await act(async () => lookup.answer({ claude: "acct-a" }));
			expect(useUiStore.getState().loginReturn).toMatchObject({ accountPending: false, accountId: "acct-a" });
			expect(useUiStore.getState().loginReturn?.loggedInAt).toBeUndefined();
			expect(retry).not.toHaveBeenCalled();
			act(() => useUiStore.getState().completeLoginReturn("claude-code", undefined, "acct-a"));
			expect(retry).toHaveBeenCalledWith("turn-1");
		} finally {
			lookup.get.mockRestore();
		}
	});

	it("counts a login that took while it read the account, once that is the account", async () => {
		const lookup = slowAccountLookup();
		try {
			const retry = vi.fn();
			render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
			await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));

			// Settings reports the login once, before the account is known.
			act(() => useUiStore.getState().completeLoginReturn("claude-code", undefined, "acct-a"));
			expect(retry).not.toHaveBeenCalled();

			await act(async () => lookup.answer({ claude: "acct-a" }));
			expect(useUiStore.getState().settingsModal).toBeNull();
			expect(retry).toHaveBeenCalledWith("turn-1");
		} finally {
			lookup.get.mockRestore();
		}
	});

	it("takes any login for the agent when the session's account cannot be read", async () => {
		const get = vi.spyOn(apiClient, "GET").mockImplementation((async (path: string) => {
			if (path === "/api/v1/agents/accounts/sessions/{sessionId}") throw new Error("daemon unreachable");
			return { data: undefined };
		}) as never);
		try {
			const retry = vi.fn();
			render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
			await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));
			await waitFor(() => expect(useUiStore.getState().loginReturn?.accountPending).toBe(false));
			expect(useUiStore.getState().loginReturn?.accountId).toBeUndefined();

			act(() => useUiStore.getState().completeLoginReturn("claude-code", undefined, "device"));
			expect(retry).toHaveBeenCalledWith("turn-1");
		} finally {
			get.mockRestore();
		}
	});

	it("starts signed out again when the agent refuses the message sent again", async () => {
		const retry = vi.fn();
		const { rerender } = render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
		await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));
		act(() => useUiStore.getState().completeLoginReturn("claude-code"));
		expect(retry).toHaveBeenCalledWith("turn-1");
		await act(async () => {});

		// The chat looked away while the copy ran, and comes back to it refused too.
		const snapshot = signedOutSnapshot({ didWork: false });
		const first = snapshot.turns[0]!;
		const prompt = snapshot.items[0]!;
		snapshot.turns = [first, { ...first, id: "turn-2", providerTurnId: "provider-turn-2", retryOfTurnId: "turn-1" }];
		snapshot.items = [prompt, { ...prompt, id: "user-retry", turnId: "turn-2", sequence: prompt.sequence + 1 } as typeof prompt];
		rerender(<ChatWorkspace snapshot={snapshot} retryControl={{ retry }} />);

		// That login did not fix it, so the band offers it again rather than "Logged in".
		expect(band()).toHaveAttribute("data-login-state", "signedOut");
		expect(within(band()).getByRole("button", { name: "Log in" })).toBeInTheDocument();
		expect(useUiStore.getState().loginReturn).toBeNull();
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute("aria-readonly", "true");
	});

	it("offers a refused message again when history paging left its prompt out", async () => {
		const snapshot = signedOutSnapshot({ didWork: true });
		// Only the turn's later work is loaded; the daemon says it has a prompt.
		snapshot.turns = [{ ...snapshot.turns[0]!, hasHumanPrompt: true }];
		snapshot.items = snapshot.items.filter((item) => !(item.kind === "message" && item.role === "user"));
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry }} />);

		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));
		expect(retry).toHaveBeenCalledWith("turn-1");
	});

	it("stops dictation when the chat box locks, and offers none while it is locked", async () => {
		const platform = Object.getOwnPropertyDescriptor(window.navigator, "platform");
		Object.defineProperty(window.navigator, "platform", { configurable: true, get: () => "MacIntel" });
		let emit: ((event: { type: "text"; text: string }) => void) | undefined;
		const onEvent = vi.spyOn(aoBridge.dictation, "onEvent").mockImplementation((listener) => {
			emit = listener as typeof emit;
			return () => {
				emit = undefined;
			};
		});
		const cancel = vi.spyOn(aoBridge.dictation, "cancel");
		act(() => useUiStore.setState({ developerMode: true }));
		try {
			const signedOut = signedOutSnapshot({ didWork: false });
			// The same chat a moment before, its message still answered.
			const answered: ConversationSnapshot = {
				...signedOut,
				turns: [{ ...signedOut.turns[0]!, state: "completed", failureCode: undefined, errorMessage: undefined }],
				account: undefined,
			};
			const { rerender } = render(<ChatWorkspace snapshot={answered} retryControl={{ retry: vi.fn() }} />);
			await userEvent.click(screen.getByRole("button", { name: "Dictate" }));
			expect(emit).toBeDefined();

			rerender(<ChatWorkspace snapshot={signedOut} retryControl={{ retry: vi.fn() }} />);
			// Locked while the user spoke: it stops listening, so no later words reach the box.
			expect(cancel).toHaveBeenCalled();
			expect(emit).toBeUndefined();
			expect(screen.getByRole("button", { name: "Dictate" })).toBeDisabled();
		} finally {
			onEvent.mockRestore();
			cancel.mockRestore();
			act(() => useUiStore.setState({ developerMode: false }));
			if (platform) Object.defineProperty(window.navigator, "platform", platform);
			else Reflect.deleteProperty(window.navigator, "platform");
		}
	});

	it("keeps Retry on an earlier refused message, which the band no longer offers", async () => {
		const snapshot = signedOutSnapshot({ didWork: false });
		const first = snapshot.turns[0]!;
		const prompt = snapshot.items[0]!;
		// Logged in some other way, the user went on with a new message, which was answered.
		snapshot.turns = [first, { ...first, id: "turn-2", providerTurnId: "provider-turn-2", state: "completed", failureCode: undefined, errorMessage: undefined }];
		snapshot.items = [prompt, { ...prompt, id: "user-2", turnId: "turn-2", sequence: prompt.sequence + 1 } as typeof prompt];
		snapshot.account = undefined;
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry }} />);

		expect(screen.queryByTestId("login-band")).toBeNull();
		expect(screen.getByTestId("login-status")).toHaveTextContent(/^Stopped$/);
		// Sending it again is the user's click, as for any failed turn.
		await userEvent.click(screen.getByRole("button", { name: "Retry this turn" }));
		expect(retry).toHaveBeenCalledWith("turn-1");
	});

	it("drops the wait, and sends nothing, once the conversation has moved on", () => {
		const snapshot = signedOutSnapshot({ didWork: false });
		const first = snapshot.turns[0]!;
		const prompt = snapshot.items[0]!;
		// The user sent it again by hand while Settings was open, and it was answered.
		snapshot.turns = [first, { ...first, id: "turn-2", providerTurnId: "provider-turn-2", state: "completed", failureCode: undefined, errorMessage: undefined }];
		snapshot.items = [prompt, { ...prompt, id: "user-2", turnId: "turn-2", sequence: prompt.sequence + 1 } as typeof prompt];
		act(() => {
			useUiStore.getState().openGlobalSettings("harness", { focusAgentId: "claude-code", startLogin: true });
			useUiStore.setState({ loginReturn: { sessionId: snapshot.sessionId, agentId: "claude-code" } });
		});
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry }} />);
		expect(screen.queryByTestId("login-band")).toBeNull();

		// The login still brings the user back, and then nothing waits on it.
		act(() => useUiStore.getState().completeLoginReturn("claude-code"));
		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(retry).not.toHaveBeenCalled();
		expect(useUiStore.getState().loginReturn).toBeNull();
	});

	it("forgets the banner's login once settings close without it", async () => {
		const snapshot = signedOutSnapshot({ didWork: false });
		snapshot.turns = [{ ...snapshot.turns[0]!, failureCode: undefined, errorMessage: "Failed to authenticate: OAuth session expired and could not be refreshed" }];
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		await userEvent.click(screen.getByRole("button", { name: "Log in again" }));
		expect(useUiStore.getState().loginReturn).not.toBeNull();

		// Given up: a later refusal starts signed out, not waiting on this login.
		act(() => useUiStore.getState().closeSettings());
		expect(useUiStore.getState().loginReturn).toBeNull();
	});

	it("brings the user back from the banner's login too, sending nothing", async () => {
		// A lapsed login without a refused message: the banner offers the login.
		const snapshot = signedOutSnapshot({ didWork: false });
		snapshot.turns = [{ ...snapshot.turns[0]!, failureCode: undefined, errorMessage: "Failed to authenticate: OAuth session expired and could not be refreshed" }];
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry }} />);

		await userEvent.click(screen.getByRole("button", { name: "Log in again" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ section: "harness", focusAgentId: "claude-code", startLogin: true });
		expect(useUiStore.getState().loginReturn).toMatchObject({ sessionId: snapshot.sessionId, agentId: "claude-code" });

		act(() => useUiStore.getState().completeLoginReturn("claude-code"));
		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(useUiStore.getState().loginReturn).toBeNull();
		expect(retry).not.toHaveBeenCalled();
	});

	it("says the user is in and leaves a message that did work for them to send again", async () => {
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: true })} retryControl={{ retry }} />);
		expect(band()).toHaveTextContent("Claude Code is signed out · send your message again once you’re in");
		expect(screen.getByText("Stopped · send it again once you’re in")).toBeInTheDocument();

		await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));
		act(() => useUiStore.getState().completeLoginReturn("claude-code"));

		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(retry).not.toHaveBeenCalled();
		expect(band()).toHaveTextContent("Logged in to Claude Code · send your message again");
		// Sending it again is the user's click, so they need not type it again.
		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));
		expect(retry).toHaveBeenCalledOnce();
		expect(retry).toHaveBeenCalledWith("turn-1");
	});

	it("keeps a cloud chat open, since nothing here watches its login", () => {
		const session = {
			id: chatFixture.sessionId, workspaceId: "project-1", workspaceName: "agent-orchestrator", title: "Cloud chat",
			provider: "claude-code", kind: "worker", mode: "chat", status: "working", updatedAt: "2026-08-15T00:00:00Z",
			activity: { state: "active", lastActivityAt: "2026-08-15T00:00:00Z" }, prs: [], runtimeConnected: true,
			cloud: { orgId: "org-1", sandboxProvider: "freestyle", desiredState: "running", observedState: "running" },
		} satisfies WorkspaceSession;
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} session={session} retryControl={{ retry: vi.fn() }} />);

		expect(band()).toHaveTextContent("Claude Code is signed out · send your message again once you’re in");
		expect(screen.getByLabelText("Message the agent")).not.toHaveAttribute("aria-readonly");
	});

	it("waits for the login on the session's own host", async () => {
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} remoteHostId="host-a" retryControl={{ retry }} />);
		expect(band()).toHaveTextContent("Claude Code is signed out on host-a");

		await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));
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

	it("offers only the latest refused message, so a message sent while signed out is the one that queues", () => {
		const snapshot = signedOutSnapshot({ didWork: false });
		const first = snapshot.turns[0]!;
		const firstPrompt = snapshot.items[0]!;
		snapshot.turns = [first, { ...first, id: "turn-2", providerTurnId: "provider-turn-2" }];
		snapshot.items = [firstPrompt, { ...firstPrompt, id: "user-2", turnId: "turn-2", sequence: firstPrompt.sequence + 1 } as typeof firstPrompt];
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		const [earlier, latest] = statusLines();
		expect(earlier).toHaveTextContent(/^Stopped$/);
		expect(latest).toHaveTextContent("Queued");
		expect(screen.getAllByTestId("login-band")).toHaveLength(1);
	});

	it("runs the queued message itself once it is sent again: one message, no band", () => {
		const snapshot = signedOutSnapshot({ didWork: false });
		const first = snapshot.turns[0]!;
		const prompt = snapshot.items[0]!;
		snapshot.turns = [first, { ...first, id: "turn-2", providerTurnId: "provider-turn-2", state: "running", failureCode: undefined, errorMessage: undefined, completedAt: undefined, retryOfTurnId: "turn-1" }];
		snapshot.items = [prompt, { ...prompt, id: "user-retry", turnId: "turn-2", sequence: prompt.sequence + 1 } as typeof prompt];
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		expect(screen.queryByTestId("login-band")).toBeNull();
		// The copy the retry sent is that message, so the refused original is not drawn beside it.
		expect(document.querySelectorAll("[data-chat-message-role=user]")).toHaveLength(1);
		expect(document.querySelector('[data-chat-message-id="user-retry"]')).toBeInTheDocument();
		expect(screen.queryByTestId("login-status")).toBeNull();
	});

	it("keeps a refused message that did work in the chat once it is sent again", () => {
		const snapshot = signedOutSnapshot({ didWork: true });
		const first = snapshot.turns[0]!;
		const prompt = snapshot.items.find((item) => item.kind === "message" && item.role === "user")!;
		const last = Math.max(...snapshot.items.map((item) => item.sequence));
		// Sent again from the signed-out band, and refused straight away.
		snapshot.turns = [first, { ...first, id: "turn-2", providerTurnId: "provider-turn-2", diff: undefined, retryOfTurnId: "turn-1" }];
		snapshot.items = [...snapshot.items, { ...prompt, id: "user-retry", turnId: "turn-2", sequence: last + 1 } as typeof prompt];
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		// The command and the edit happened, so they stay, with the message that ran them.
		expect(document.querySelector(`[data-chat-message-id="${prompt.id}"]`)).toBeInTheDocument();
		expect(document.querySelector("[data-turn-worked]")).toBeInTheDocument();
		expect(document.querySelector('[data-chat-message-id="user-retry"]')).toBeInTheDocument();
	});

	it("keeps the banner when the latest turn did not fail on the login", () => {
		const snapshot = signedOutSnapshot({ didWork: false });
		snapshot.turns = [{ ...snapshot.turns[0]!, failureCode: undefined, errorMessage: "stream disconnected" }];
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		expect(screen.getByRole("button", { name: "Log in again" })).toBeInTheDocument();
		expect(screen.queryByTestId("login-band")).toBeNull();
		expect(screen.getByText("The agent ran into a problem")).toBeInTheDocument();
	});
});
