import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { chatFixture, chatFixtureEmpty, chatFixtureSettled } from "../../lib/chat-fixture";
import type { ComposerMention } from "../../lib/cloud-team-collab";
import { readChatSessionDraft } from "../../lib/chat-drafts";
import { useUiStore } from "../../stores/ui-store";
import { lexicalEditorText, typeInLexicalEditor } from "../../test/lexical";
import { renderChat as render } from "../../test/render-chat";
import type { ConversationActivity, ConversationSnapshot } from "../../types/conversation";
import { ChatWorkspace } from "./ChatWorkspace";
import type { ComposerMode } from "./ComposerModeToggle";

vi.mock("../NotificationCenter", () => ({ NotificationCenter: () => <button type="button" aria-label="Notifications" /> }));
// Draft storage can be refused (another window, no storage): the composer then keeps drafts in memory.
const drafts = vi.hoisted(() => ({ refuse: false }));
vi.mock("../../lib/chat-drafts", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../../lib/chat-drafts")>();
	return {
		...actual,
		activateChatDraftScope: (...args: Parameters<typeof actual.activateChatDraftScope>) =>
			drafts.refuse ? { ok: false as const, reason: "storage" as const } : actual.activateChatDraftScope(...args),
	};
});

const aside: ConversationActivity = {
	kind: "activity", id: "cloud-aside-a1", sequence: 900, revision: 1, activityKind: "system", status: "completed",
	summary: "Ping me before you merge.", createdAt: "2026-11-07T10:00:00Z",
	detail: { event: "chat.aside", aside: { asideId: "a1", text: "Ping me before you merge.", authorUserId: "u-asha", authorName: "Asha Rao", mentions: [] } },
};

type WorkspaceProps = Parameters<typeof ChatWorkspace>[0];

function Harness({ sessionId, onSend, onSendAside, onComposerInput, initialMode = "agent", edit, snapshot, extra }: {
	sessionId: string;
	onSend: NonNullable<WorkspaceProps["onSend"]>;
	onSendAside: NonNullable<WorkspaceProps["composerModes"]>["onSendAside"];
	onComposerInput?: WorkspaceProps["onComposerInput"];
	initialMode?: ComposerMode;
	edit?: { id: string; text: string; mentions?: ComposerMention[] };
	snapshot?: ConversationSnapshot;
	extra?: Partial<WorkspaceProps>;
}) {
	const [mode, setMode] = useState<ComposerMode>(initialMode);
	return (
		<ChatWorkspace
			snapshot={snapshot ?? { ...chatFixtureSettled, sessionId }}
			onSend={onSend}
			onComposerInput={onComposerInput}
			composerModes={{ mode, onModeChange: setMode, onSendAside }}
			composerEdit={edit}
			{...extra}
		/>
	);
}

const shiftTab = () => new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true });

beforeEach(() => {
	drafts.refuse = false;
	localStorage.clear();
	useUiStore.setState({ chatSendKeyMode: "enter" });
});

describe("the composer's Agent and Aside modes", () => {
	it("switches with the toggle and keeps a draft for each mode", async () => {
		const onSend = vi.fn();
		const onSendAside = vi.fn(async () => undefined);
		render(<Harness sessionId="modes-drafts" onSend={onSend} onSendAside={onSendAside} />);
		const agent = screen.getByRole("combobox", { name: "Message the agent" });
		await typeInLexicalEditor(agent, "for the agent");
		fireEvent.click(screen.getByRole("radio", { name: "Aside" }));
		const aside = await screen.findByRole("combobox", { name: "Message teammates" });
		expect(lexicalEditorText(aside)).toBe("");
		// The composer itself changes, not only its words: an aside's edge and a quiet hint.
		expect(aside.closest("form")).toHaveAttribute("data-composer-variant", "aside");
		expect(aside.closest("form")?.querySelector("[data-aside-composer-hint]")).toHaveTextContent("Agent can't see this");
		// The placeholder is its label; the footer hint says the rest, once.
		expect(screen.queryByText(/The agent never reads asides/)).toBeNull();
		await typeInLexicalEditor(aside, "for people");
		fireEvent.click(screen.getByRole("radio", { name: "Agent" }));
		const back = await screen.findByRole("combobox", { name: "Message the agent" });
		expect(back.closest("form")).not.toHaveAttribute("data-composer-variant");
		expect(document.querySelector("[data-aside-composer-hint]")).toBeNull();
		await waitFor(() => expect(lexicalEditorText(back)).toBe("for the agent"));
		expect(readChatSessionDraft({ sessionId: "modes-drafts#aside", incarnation: "modes-drafts" }).composer.text).toBe("for people");
		expect(readChatSessionDraft("modes-drafts").composer.text).toBe("for the agent");
		fireEvent.click(screen.getByRole("radio", { name: "Aside" }));
		const again = await screen.findByRole("combobox", { name: "Message teammates" });
		await waitFor(() => expect(lexicalEditorText(again)).toBe("for people"));
	});

	it("switches on Shift+Tab in the editor", async () => {
		render(<Harness sessionId="modes-shift-tab" onSend={vi.fn()} onSendAside={vi.fn()} />);
		const agent = screen.getByRole("combobox", { name: "Message the agent" });
		act(() => { agent.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true })); });
		const aside = await screen.findByRole("combobox", { name: "Message teammates" });
		act(() => { aside.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true })); });
		expect(await screen.findByRole("combobox", { name: "Message the agent" })).toBeInTheDocument();
	});

	it("posts an aside instead of a message, and takes no composer lease while typing it", async () => {
		const onSend = vi.fn();
		const onSendAside = vi.fn(async () => undefined);
		const onComposerInput = vi.fn();
		render(<Harness sessionId="modes-send" onSend={onSend} onSendAside={onSendAside} onComposerInput={onComposerInput} initialMode="aside" />);
		const aside = screen.getByRole("combobox", { name: "Message teammates" });
		await typeInLexicalEditor(aside, "Looks right");
		expect(onComposerInput).not.toHaveBeenCalled();
		fireEvent.keyDown(aside, { key: "Enter" });
		await waitFor(() => expect(onSendAside).toHaveBeenCalledWith("Looks right", expect.any(String), undefined));
		expect(onSend).not.toHaveBeenCalled();
	});

	it("stays open while someone else holds the agent's composer", async () => {
		const onSendAside = vi.fn(async () => undefined);
		render(
			<ChatWorkspace
				snapshot={{ ...chatFixtureSettled, sessionId: "modes-locked" }}
				composerLockedPlaceholder=""
				composerModes={{ mode: "aside", onModeChange: vi.fn(), onSendAside }}
			/>,
		);
		const aside = screen.getByRole("combobox", { name: "Message teammates" });
		expect(aside).not.toHaveAttribute("aria-disabled");
		await typeInLexicalEditor(aside, "Chen can talk while Ben drives");
		fireEvent.keyDown(aside, { key: "Enter" });
		await waitFor(() => expect(onSendAside).toHaveBeenCalled());
	});

	it("puts an edited suggestion in place of the driver's draft, and gives the draft back on Cancel", async () => {
		const props = { sessionId: "modes-edit", onSend: vi.fn(), onSendAside: vi.fn() };
		const { rerender } = render(<Harness {...props} />);
		await typeInLexicalEditor(screen.getByRole("combobox", { name: "Message the agent" }), "my own draft");
		rerender(<Harness {...props} edit={{ id: "sg-1:1", text: "Cover the expired cookie" }} />);
		// Replaced, never appended to the driver's words.
		await waitFor(() => expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message the agent" }))).toBe("Cover the expired cookie"));
		expect(screen.getByRole("combobox", { name: "Message the agent" })).toHaveFocus();
		expect(readChatSessionDraft("modes-edit").composer.text).toBe("my own draft");
		// Cancel: the suggestion is gone from the composer, the draft is back.
		rerender(<Harness {...props} />);
		await waitFor(() => expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message the agent" }))).toBe("my own draft"));
		// Edit again works, from the suggestion's own words.
		rerender(<Harness {...props} edit={{ id: "sg-1:2", text: "Cover the expired cookie" }} />);
		await waitFor(() => expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message the agent" }))).toBe("Cover the expired cookie"));
		rerender(<Harness {...props} />);
		await waitFor(() => expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message the agent" }))).toBe("my own draft"));
	});

	it("sends an edited suggestion with its chips, and comes back to the driver's draft", async () => {
		const onSend = vi.fn(async (..._args: unknown[]) => undefined);
		const chen: ComposerMention = { kind: "person", id: "u-chen", name: "Chen Wei" };
		const props = { sessionId: "modes-edit-send", onSend, onSendAside: vi.fn(), extra: { composerMentions: { onQuery: vi.fn() } } };
		const { rerender } = render(<Harness {...props} />);
		await typeInLexicalEditor(screen.getByRole("combobox", { name: "Message the agent" }), "mine");
		rerender(<Harness {...props} edit={{ id: "sg-2:1", text: "@Chen Wei check the cookie", mentions: [chen] }} />);
		const editor = screen.getByRole("combobox", { name: "Message the agent" });
		await waitFor(() => expect(editor.querySelector('[data-composer-token="person"]')).toHaveTextContent("Chen Wei"));
		fireEvent.keyDown(editor, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]![0]).toBe("@Chen Wei check the cookie");
		// The workspace's onSend takes the mentions last, after the display text.
		expect(onSend.mock.calls[0]![6]).toEqual([chen]);
		// The send never touched the driver's saved draft.
		expect(readChatSessionDraft("modes-edit-send").composer.text).toBe("mine");
	});

	it("keeps both drafts in memory when draft storage is refused", async () => {
		drafts.refuse = true;
		render(<Harness sessionId="modes-memory" onSend={vi.fn()} onSendAside={vi.fn()} />);
		await typeInLexicalEditor(await screen.findByRole("combobox", { name: "Message the agent" }), "agent words");
		fireEvent.click(screen.getByRole("radio", { name: "Aside" }));
		await typeInLexicalEditor(await screen.findByRole("combobox", { name: "Message teammates" }), "aside words");
		fireEvent.click(screen.getByRole("radio", { name: "Agent" }));
		await waitFor(() => expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message the agent" }))).toBe("agent words"));
		fireEvent.click(screen.getByRole("radio", { name: "Aside" }));
		await waitFor(() => expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message teammates" }))).toBe("aside words"));
	});

	it("gives a viewer the aside composer only: no switch, and Shift+Tab moves focus as usual", async () => {
		render(
			<ChatWorkspace
				snapshot={{ ...chatFixtureSettled, sessionId: "modes-viewer" }}
				composerLockedPlaceholder="You can view this session but not write to the agent"
				composerModes={{ mode: "aside", onSendAside: vi.fn() }}
			/>,
		);
		const aside = screen.getByRole("combobox", { name: "Message teammates" });
		expect(screen.queryByRole("radiogroup", { name: "Write to" })).toBeNull();
		const event = shiftTab();
		act(() => { aside.dispatchEvent(event); });
		expect(event.defaultPrevented).toBe(false);
		expect(screen.getByRole("combobox", { name: "Message teammates" })).toBeInTheDocument();
	});

	it("puts the switch on an approval that took the composer's place, so the team can talk it over", async () => {
		const snapshot = { ...structuredClone(chatFixture), sessionId: "modes-approval" };
		render(<Harness sessionId="modes-approval" onSend={vi.fn()} onSendAside={vi.fn()} snapshot={snapshot} />);
		const footer = document.querySelector("[data-approval-dock-footer]")!;
		expect(footer).not.toBeNull();
		fireEvent.click(screen.getByRole("radio", { name: "Aside" }));
		expect(await screen.findByRole("combobox", { name: "Message teammates" })).toBeInTheDocument();
		// The approval still waits: Agent says it needs you.
		expect(screen.getByRole("radio", { name: "Agent, needs you" })).toHaveAttribute("data-needs-you");
		// Shift+Tab back to the approval.
		act(() => { screen.getByRole("combobox", { name: "Message teammates" }).dispatchEvent(shiftTab()); });
		await waitFor(() => expect(document.querySelector("[data-approval-dock-footer]")).not.toBeNull());
		act(() => { (document.querySelector("[data-approval-dock-footer]") as HTMLElement).dispatchEvent(shiftTab()); });
		expect(await screen.findByRole("combobox", { name: "Message teammates" })).toBeInTheDocument();
	});

	it("says asides are hidden on this device in Aside mode's footer, with Show", async () => {
		const onShowAsides = vi.fn();
		render(
			<ChatWorkspace
				snapshot={{ ...chatFixtureSettled, sessionId: "modes-hidden" }}
				composerModes={{ mode: "aside", onModeChange: vi.fn(), onSendAside: vi.fn(), asidesHidden: true, onShowAsides }}
			/>,
		);
		expect(document.querySelector("[data-aside-hidden-notice]")).toHaveTextContent("Asides are hidden · Show");
		fireEvent.click(screen.getByRole("button", { name: "Show" }));
		expect(onShowAsides).toHaveBeenCalled();
	});

	it("adds a quote to the aside on screen in Aside mode, never to the hidden agent draft", async () => {
		render(<Harness sessionId={chatFixtureSettled.sessionId} onSend={vi.fn()} onSendAside={vi.fn()} initialMode="aside" extra={{ excerptsEnabled: true }} />);
		const log = screen.getByRole("log", { name: "Conversation" });
		const source = log.querySelector("[data-chat-message-id] [data-chat-message-body]")!;
		const range = document.createRange();
		range.selectNodeContents(source);
		const quoted = range.toString().trim();
		const selection = window.getSelection()!;
		selection.removeAllRanges();
		selection.addRange(range);
		vi.spyOn(Range.prototype, "getClientRects").mockReturnValue([new DOMRect(40, 150, 100, 20)] as unknown as DOMRectList);
		vi.spyOn(Range.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(40, 150, 100, 20));
		vi.spyOn(log, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 600, 500));
		fireEvent.pointerUp(document.body);
		fireEvent.click(screen.getByRole("button", { name: "Add to chat" }));
		await waitFor(() => expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message teammates" }))).toContain(quoted.split("\n")[0]!));
		expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message teammates" })).startsWith("> ")).toBe(true);
		expect(readChatSessionDraft(chatFixtureSettled.sessionId).composer.excerpts ?? []).toHaveLength(0);
		selection.removeAllRanges();
		vi.restoreAllMocks();
	});

	it("keeps the chips of a queued message being edited", async () => {
		const snapshot: ConversationSnapshot = {
			...chatFixtureEmpty,
			sessionId: "modes-queued",
			turns: [
				{ id: "running", state: "running", requestedAt: "2026-09-07T00:00:00Z" },
				{ id: "queued", state: "queued", requestedAt: "2026-09-07T00:00:00Z" },
			],
			items: [{ kind: "message", id: "queued-message", turnId: "queued", role: "user", origin: "human", streaming: false, text: "queued text", sequence: 1, revision: 0, createdAt: "2026-09-07T00:00:00Z" }],
		};
		const save = vi.fn().mockResolvedValue(undefined);
		const candidates = { people: [{ userId: "u-chen", name: "Chen Wei", inSession: true, canOpen: true }], teams: [] };
		render(<ChatWorkspace snapshot={snapshot} onSend={vi.fn()} onEditQueuedTurn={save} composerMentions={{ onQuery: vi.fn(), candidates }} />);
		await userEvent.click(screen.getByRole("button", { name: "Edit queued message" }));
		const editor = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(editor, " @che");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(editor.querySelector('[data-composer-token="person"]')).not.toBeNull());
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(save).toHaveBeenCalled());
		expect(save.mock.calls[0]![2]).toMatchObject({ mentions: [{ kind: "person", id: "u-chen", name: "Chen Wei" }] });
	});

	it("draws an aside in the timeline as a note the agent can't see", () => {
		const snapshot = { ...chatFixtureSettled, sessionId: "modes-timeline", items: [...chatFixtureSettled.items, aside] };
		render(<Harness sessionId="modes-timeline" onSend={vi.fn()} onSendAside={vi.fn()} snapshot={snapshot} />);
		expect(screen.getByText("Ping me before you merge.")).toBeInTheDocument();
		expect(screen.getByRole("img", { name: "Agent can't see this" })).toBeInTheDocument();
	});

	it("shows no mode toggle without modes", () => {
		render(<ChatWorkspace snapshot={{ ...chatFixtureSettled, sessionId: "modes-none" }} />);
		expect(screen.queryByRole("radiogroup", { name: "Write to" })).toBeNull();
	});
});
