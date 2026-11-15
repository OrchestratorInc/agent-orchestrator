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
const statusLines = () => screen.getAllByTestId("login-status");

afterEach(() => {
	act(() => useUiStore.setState({ settingsModal: null, loginReturn: null }));
});

describe("ChatWorkspace refused login", () => {
	it("moves the login onto the chat box and leaves a queued line under the message", () => {
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry: vi.fn() }} />);

		expect(band()).toHaveTextContent("Claude Code is signed out · your message sends once you’re in");
		expect(band().closest(".chat-composer-band-tray")).toBeInTheDocument();
		expect(document.querySelector("[data-composer-tray]")).toBeNull();
		const box = screen.getByLabelText("Message the agent");
		expect(box).toHaveAttribute("aria-readonly", "true");
		expect(box).not.toHaveAttribute("aria-disabled");
		expect(box).toHaveAttribute("aria-placeholder", "Message the agent");
		expect(box).not.toHaveClass("opacity-50");
		expect(within(band()).getByRole("button", { name: "Log in" })).toBeInTheDocument();
		const line = screen.getByTestId("login-status");
		expect(line).toHaveTextContent("Queued");
		expect(line.closest("[data-chat-message-id]")).toBeInTheDocument();
		expect(line.closest("[data-turn-outcome]")).toBeNull();
		const row = line.parentElement!.parentElement!;
		expect(row).toContainElement(screen.getByRole("button", { name: "Copy user message" }));
		expect(within(row).getByLabelText(/^Sent /)).toBeInTheDocument();
		expect(screen.queryByText(CLI_ADVICE)).toBeNull();
		expect(screen.queryByText("The agent ran into a problem")).toBeNull();
		expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Log in again" })).toBeNull();
		expect(document.querySelector("[data-turn-worked]")).toBeNull();
	});

	it("opens the Harness page on the login, then sends the message again once it takes", async () => {
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);

		await userEvent.click(within(band()).getByRole("button", { name: "Log in" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({
			scope: "global", section: "harness", focusAgentId: "claude-code", harnessView: "local", startLogin: true,
		});
		expect(band()).toHaveTextContent("Finish logging in on the Harness page · your message is waiting");
		expect(within(band()).getByRole("button", { name: "Open again" })).toBeInTheDocument();
		expect(screen.getByTestId("login-status")).toHaveTextContent("Waiting for login");
		expect(retry).not.toHaveBeenCalled();

		act(() => useUiStore.getState().completeLoginReturn("claude-code"));

		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(retry).toHaveBeenCalledOnce();
		expect(retry).toHaveBeenCalledWith("turn-1");
		expect(useUiStore.getState().loginReturn?.settled).toBe(true);
		expect(screen.getByLabelText("Message the agent")).not.toHaveAttribute("aria-readonly");
	});

	it("says it is sending from the moment it sends the message again, with nothing to click twice", async () => {
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

		expect(band()).toHaveAttribute("data-login-state", "waiting");
		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));
		expect(retry).toHaveBeenCalledOnce();
		expect(retry).toHaveBeenCalledWith("turn-1");
	});

	it("offers Send again while signed out, for a login fixed before the daemon looks again", async () => {
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);

		expect(band()).toHaveAttribute("data-login-state", "signedOut");
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
		expect(band()).toHaveTextContent("Sending…");
		expect(band()).not.toHaveTextContent("Logged in");
	});

	it("says when Send again failed while signed out, and keeps the login on offer", async () => {
		const retry = vi.fn();
		const { rerender } = render(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry }} />);
		await userEvent.click(within(band()).getByRole("button", { name: "Send again" }));

		rerender(<ChatWorkspace snapshot={signedOutSnapshot({ didWork: false })} retryControl={{ retry, error: "The daemon is not reachable", turnId: "turn-1" }} />);
		expect(band()).toHaveAttribute("data-login-state", "failed");
		expect(band()).toHaveTextContent("Couldn’t send your message · The daemon is not reachable");
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
		const before = { ...snapshot, account: { authenticationState: "authenticated" as const, authVerifiedAt: new Date(refusedAt - 60_000).toISOString() } };
		const { rerender } = render(<ChatWorkspace snapshot={before} retryControl={{ retry }} />);
		expect(band()).toHaveAttribute("data-login-state", "signedOut");
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute("aria-readonly", "true");

		const after = { ...snapshot, account: { authenticationState: "authenticated" as const, authVerifiedAt: new Date(refusedAt + 60_000).toISOString() } };
		rerender(<ChatWorkspace snapshot={after} retryControl={{ retry }} />);

		expect(screen.getByLabelText("Message the agent")).not.toHaveAttribute("aria-readonly");
		expect(band()).toHaveTextContent("Logged in to Claude Code · send your message again");
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
			expect(get).toHaveBeenCalledWith("/api/v1/agents/accounts/sessions/{sessionId}", expect.anything());
			await waitFor(() => expect(useUiStore.getState().loginReturn?.accountId).toBe("acct-a"));

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

			act(() => useUiStore.getState().completeLoginReturn("claude-code", undefined, "device"));
			expect(useUiStore.getState().loginReturn?.loggedInAt).toBeUndefined();
			expect(useUiStore.getState().settingsModal).not.toBeNull();
			expect(retry).not.toHaveBeenCalled();

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

		const snapshot = signedOutSnapshot({ didWork: false });
		const first = snapshot.turns[0]!;
		const prompt = snapshot.items[0]!;
		snapshot.turns = [first, { ...first, id: "turn-2", providerTurnId: "provider-turn-2", retryOfTurnId: "turn-1" }];
		snapshot.items = [prompt, { ...prompt, id: "user-retry", turnId: "turn-2", sequence: prompt.sequence + 1 } as typeof prompt];
		rerender(<ChatWorkspace snapshot={snapshot} retryControl={{ retry }} />);

		expect(band()).toHaveAttribute("data-login-state", "signedOut");
		expect(within(band()).getByRole("button", { name: "Log in" })).toBeInTheDocument();
		expect(useUiStore.getState().loginReturn).toBeNull();
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute("aria-readonly", "true");
	});

	it("offers a refused message again when history paging left its prompt out", async () => {
		const snapshot = signedOutSnapshot({ didWork: true });
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
			const answered: ConversationSnapshot = {
				...signedOut,
				turns: [{ ...signedOut.turns[0]!, state: "completed", failureCode: undefined, errorMessage: undefined }],
				account: undefined,
			};
			const { rerender } = render(<ChatWorkspace snapshot={answered} retryControl={{ retry: vi.fn() }} />);
			await userEvent.click(screen.getByRole("button", { name: "Dictate" }));
			expect(emit).toBeDefined();

			rerender(<ChatWorkspace snapshot={signedOut} retryControl={{ retry: vi.fn() }} />);
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
		snapshot.turns = [first, { ...first, id: "turn-2", providerTurnId: "provider-turn-2", state: "completed", failureCode: undefined, errorMessage: undefined }];
		snapshot.items = [prompt, { ...prompt, id: "user-2", turnId: "turn-2", sequence: prompt.sequence + 1 } as typeof prompt];
		snapshot.account = undefined;
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry }} />);

		expect(screen.queryByTestId("login-band")).toBeNull();
		expect(screen.getByTestId("login-status")).toHaveTextContent(/^Stopped$/);
		await userEvent.click(screen.getByRole("button", { name: "Retry this turn" }));
		expect(retry).toHaveBeenCalledWith("turn-1");
	});

	it("drops the wait, and sends nothing, once the conversation has moved on", () => {
		const snapshot = signedOutSnapshot({ didWork: false });
		const first = snapshot.turns[0]!;
		const prompt = snapshot.items[0]!;
		snapshot.turns = [first, { ...first, id: "turn-2", providerTurnId: "provider-turn-2", state: "completed", failureCode: undefined, errorMessage: undefined }];
		snapshot.items = [prompt, { ...prompt, id: "user-2", turnId: "turn-2", sequence: prompt.sequence + 1 } as typeof prompt];
		act(() => {
			useUiStore.getState().openGlobalSettings("harness", { focusAgentId: "claude-code", startLogin: true });
			useUiStore.setState({ loginReturn: { sessionId: snapshot.sessionId, agentId: "claude-code" } });
		});
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry }} />);
		expect(screen.queryByTestId("login-band")).toBeNull();

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

		act(() => useUiStore.getState().closeSettings());
		expect(useUiStore.getState().loginReturn).toBeNull();
	});

	it("brings the user back from the banner's login too, sending nothing", async () => {
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
		expect(document.querySelectorAll("[data-chat-message-role=user]")).toHaveLength(1);
		expect(document.querySelector('[data-chat-message-id="user-retry"]')).toBeInTheDocument();
		expect(screen.queryByTestId("login-status")).toBeNull();
	});

	it("keeps a refused message that did work in the chat once it is sent again", () => {
		const snapshot = signedOutSnapshot({ didWork: true });
		const first = snapshot.turns[0]!;
		const prompt = snapshot.items.find((item) => item.kind === "message" && item.role === "user")!;
		const last = Math.max(...snapshot.items.map((item) => item.sequence));
		snapshot.turns = [first, { ...first, id: "turn-2", providerTurnId: "provider-turn-2", diff: undefined, retryOfTurnId: "turn-1" }];
		snapshot.items = [...snapshot.items, { ...prompt, id: "user-retry", turnId: "turn-2", sequence: last + 1 } as typeof prompt];
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

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
