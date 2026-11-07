import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { chatFixtureSettled } from "../../lib/chat-fixture";
import { readChatSessionDraft } from "../../lib/chat-drafts";
import { useUiStore } from "../../stores/ui-store";
import { lexicalEditorText, typeInLexicalEditor } from "../../test/lexical";
import { renderChat as render } from "../../test/render-chat";
import type { ConversationActivity, ConversationSnapshot } from "../../types/conversation";
import { ChatWorkspace } from "./ChatWorkspace";
import type { ComposerMode } from "./ComposerModeToggle";

vi.mock("../NotificationCenter", () => ({ NotificationCenter: () => <button type="button" aria-label="Notifications" /> }));

const aside: ConversationActivity = {
	kind: "activity", id: "cloud-aside-a1", sequence: 900, revision: 1, activityKind: "system", status: "completed",
	summary: "Ping me before you merge.", createdAt: "2026-11-07T10:00:00Z",
	detail: { event: "chat.aside", aside: { asideId: "a1", text: "Ping me before you merge.", authorUserId: "u-asha", authorName: "Asha Rao", mentions: [] } },
};

type WorkspaceProps = Parameters<typeof ChatWorkspace>[0];

function Harness({ sessionId, onSend, onSendAside, onComposerInput, initialMode = "agent", draftRequest, snapshot }: {
	sessionId: string;
	onSend: NonNullable<WorkspaceProps["onSend"]>;
	onSendAside: NonNullable<WorkspaceProps["composerModes"]>["onSendAside"];
	onComposerInput?: WorkspaceProps["onComposerInput"];
	initialMode?: ComposerMode;
	draftRequest?: { id: string; text: string };
	snapshot?: ConversationSnapshot;
}) {
	const [mode, setMode] = useState<ComposerMode>(initialMode);
	return (
		<ChatWorkspace
			snapshot={snapshot ?? { ...chatFixtureSettled, sessionId }}
			onSend={onSend}
			onComposerInput={onComposerInput}
			composerModes={{ mode, onModeChange: setMode, onSendAside }}
			composerDraftRequest={draftRequest}
		/>
	);
}

beforeEach(() => {
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
		fireEvent.click(screen.getByRole("button", { name: "Aside" }));
		const aside = await screen.findByRole("combobox", { name: "Message teammates" });
		expect(lexicalEditorText(aside)).toBe("");
		// The composer itself changes, not only its words: an aside's edge and a quiet hint.
		expect(aside.closest("form")).toHaveAttribute("data-composer-variant", "aside");
		expect(aside.closest("form")?.querySelector("[data-aside-composer-hint]")).toHaveTextContent("Agent can't see this");
		expect(screen.getByText("Message teammates. The agent never reads asides")).toBeInTheDocument();
		await typeInLexicalEditor(aside, "for people");
		fireEvent.click(screen.getByRole("button", { name: "Agent" }));
		const back = await screen.findByRole("combobox", { name: "Message the agent" });
		expect(back.closest("form")).not.toHaveAttribute("data-composer-variant");
		expect(document.querySelector("[data-aside-composer-hint]")).toBeNull();
		await waitFor(() => expect(lexicalEditorText(back)).toBe("for the agent"));
		expect(readChatSessionDraft({ sessionId: "modes-drafts#aside", incarnation: "modes-drafts" }).composer.text).toBe("for people");
		expect(readChatSessionDraft("modes-drafts").composer.text).toBe("for the agent");
		fireEvent.click(screen.getByRole("button", { name: "Aside" }));
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

	it("adds a suggestion being edited to the agent draft once", async () => {
		const { rerender } = render(<Harness sessionId="modes-edit" onSend={vi.fn()} onSendAside={vi.fn()} draftRequest={{ id: "sg-1", text: "Cover the expired cookie" }} />);
		const agent = await screen.findByRole("combobox", { name: "Message the agent" });
		await waitFor(() => expect(lexicalEditorText(agent)).toBe("Cover the expired cookie"));
		rerender(<Harness sessionId="modes-edit" onSend={vi.fn()} onSendAside={vi.fn()} draftRequest={{ id: "sg-1", text: "Cover the expired cookie" }} />);
		expect(lexicalEditorText(agent)).toBe("Cover the expired cookie");
	});

	it("draws an aside in the timeline as a note the agent can't see", () => {
		const snapshot = { ...chatFixtureSettled, sessionId: "modes-timeline", items: [...chatFixtureSettled.items, aside] };
		render(<Harness sessionId="modes-timeline" onSend={vi.fn()} onSendAside={vi.fn()} snapshot={snapshot} />);
		expect(screen.getByText("Ping me before you merge.")).toBeInTheDocument();
		expect(screen.getByText("Agent can't see this")).toBeInTheDocument();
	});

	it("shows no mode toggle without modes", () => {
		render(<ChatWorkspace snapshot={{ ...chatFixtureSettled, sessionId: "modes-none" }} />);
		expect(screen.queryByRole("group", { name: "Write to" })).toBeNull();
	});
});
