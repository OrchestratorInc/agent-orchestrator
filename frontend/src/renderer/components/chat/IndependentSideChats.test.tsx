import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SideComposer } from "./IndependentSideChats";
import { emptySideChatDraft } from "./sideChatDraft";
import { typeInLexicalEditor } from "../../test/lexical";

const side = {
	id: "side-1",
	sessionId: "session-1",
	mainConversationId: "main-1",
	contextMode: "native",
	createdAt: "2026-01-01T00:00:00Z",
	updatedAt: "2026-01-01T00:00:00Z",
	label: "Side",
	state: "ready",
	hasWork: false,
};

function renderSideComposer(
	draft = emptySideChatDraft(),
	flags: { sending?: boolean; ready?: boolean; deliveryPending?: boolean } = {},
) {
	const onSend = vi.fn();
	const onFocusSide = vi.fn();
	const onCompact = vi.fn();
	const onNavigate = vi.fn();
	render(
		<SideComposer
			draft={draft}
			onChange={vi.fn()}
			onSend={onSend}
			onInterrupt={vi.fn()}
			onAttach={vi.fn()}
			files={[]}
			onRemoveFile={vi.fn()}
			ready
			running={false}
			sending={false}
			{...flags}
			models={[]}
			skills={[{ name: "review", displayName: "review", source: "repo" }]}
			side={side}
			onSettings={vi.fn()}
			onRemoveReference={vi.fn()}
			onFocusSide={onFocusSide}
			onCompact={onCompact}
			onNavigate={onNavigate}
			focusKey={0}
		/>,
	);
	return {
		onSend,
		onFocusSide,
		onCompact,
		onNavigate,
		field: screen.getByLabelText("Side chat question") as HTMLElement,
	};
}

describe("side composer slash menu", () => {
	it("shows provider skills and opens /btw without sending", async () => {
		const { onSend, onFocusSide, field } = renderSideComposer();
		await typeInLexicalEditor(field, "/");
		expect(screen.getByRole("option", { name: /\/review/ })).toBeInTheDocument();
		expect(screen.getByRole("option", { name: /\/compact/ })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("option", { name: /\/btw/ }));
		await waitFor(() => expect(onFocusSide).toHaveBeenCalled());
		expect(onSend).not.toHaveBeenCalled();
	});

	it("shows ordered references in a removable compact summary", async () => {
		const draft = {
			...emptySideChatDraft(),
			references: [
				{
					id: "one",
					conversationId: "main",
					messageId: "m1",
					revision: 1,
					text: "sun",
					role: "assistant" as const,
				},
				{
					id: "two",
					conversationId: "side",
					messageId: "m2",
					revision: 1,
					text: "moon",
					role: "user" as const,
				},
			],
		};
		renderSideComposer(draft);
		await userEvent.click(screen.getByRole("button", { name: "2 annotations" }));
		expect(screen.getByRole("button", { name: "sun" })).toBeInTheDocument();
		expect(screen.getAllByRole("button", { name: /Remove annotation/ })).toHaveLength(2);
	});

	it("keeps a typed bare /btw in this side without sending", async () => {
		const { onSend, onFocusSide, field } = renderSideComposer();
		await typeInLexicalEditor(field, "/btw");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onFocusSide).toHaveBeenCalled());
		expect(onSend).not.toHaveBeenCalled();
	});

	it("routes /compact to this side instead of sending it as a question", async () => {
		const { onSend, onCompact, field } = renderSideComposer();
		await typeInLexicalEditor(field, "/compact");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onCompact).toHaveBeenCalledTimes(1));
		expect(onSend).not.toHaveBeenCalled();
	});
	it("keeps editing enabled during delivery and blocks uncertain sends", async () => {
		const { field, onSend } = renderSideComposer(emptySideChatDraft(), {
			sending: true,
			deliveryPending: true,
		});
		expect(field).toHaveAttribute("contenteditable", "true");
		await typeInLexicalEditor(field, "next question");
		fireEvent.keyDown(field, { key: "Enter" });
		expect(onSend).not.toHaveBeenCalled();
	});
	it("navigates to the original main selection from its compact popover", async () => {
		const reference = {
			id: "ref",
			conversationId: "main",
			messageId: "source",
			revision: 3,
			text: "selected text",
			role: "assistant" as const,
		};
		const { onNavigate } = renderSideComposer({
			...emptySideChatDraft(),
			references: [reference],
		});
		await userEvent.click(screen.getByRole("button", { name: "1 annotation" }));
		await userEvent.click(screen.getByRole("button", { name: "selected text" }));
		expect(onNavigate).toHaveBeenCalledWith(reference);
	});
	it("allows an excerpt-only send with no typed wording", async () => {
		const reference = {
			id: "ref",
			conversationId: "main",
			messageId: "source",
			revision: 3,
			text: "selected text",
			role: "user" as const,
		};
		const { onSend } = renderSideComposer({
			...emptySideChatDraft(),
			references: [reference],
		});
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		expect(onSend).toHaveBeenCalledWith("");
	});
});
