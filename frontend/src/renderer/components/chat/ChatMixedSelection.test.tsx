import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatExcerptActivation } from "../../../shared/chat-excerpt";
import { renderChat as render } from "../../test/render-chat";
import { typeInLexicalEditor } from "../../test/lexical";
import { chatFixtureSettled } from "../../lib/chat-fixture";
import { ChatWorkspace } from "./ChatWorkspace";
import type { ConversationSnapshot } from "../../types/conversation";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";

let serial = 0;
function fixture(): ConversationSnapshot {
	const base = structuredClone(chatFixtureSettled);
	const session = `mixed-ui-${serial++}`;
	return {
		...base,
		sessionId: session,
		conversationId: session,
		controller: { state: "ready" },
		turns: [
			{
				...base.turns[0],
				id: "mixed-turn",
				state: "completed",
				rolledBack: false,
			},
		],
		items: [
			{
				kind: "message",
				id: "mixed-user",
				turnId: "mixed-turn",
				sequence: 1,
				revision: 1,
				role: "user",
				origin: "human",
				text: "Explain",
				streaming: false,
				createdAt: "2026-11-01T00:00:00Z",
			},
			{
				kind: "activity",
				id: "mixed-command",
				turnId: "mixed-turn",
				sequence: 2,
				revision: 3,
				activityKind: "command",
				status: "completed",
				summary: "Ran powershell",
				detail: { command: "Write-Output 'Copy'", output: "Copied ✓\nDiagram" },
				createdAt: "2026-11-01T00:00:01Z",
			},
			{
				kind: "message",
				id: "mixed-answer",
				turnId: "mixed-turn",
				sequence: 3,
				revision: 2,
				role: "assistant",
				origin: "provider",
				text: "The Code result follows.",
				streaming: false,
				createdAt: "2026-11-01T00:00:02Z",
			},
		],
	};
}
async function selectMixed() {
	const log = screen.getByRole("log", { name: "Conversation" });
	for (const worked of screen.queryAllByRole("button", { name: /Worked for/ }))
		fireEvent.click(worked);
	await waitFor(() =>
		expect(
			log.querySelector('[data-chat-activity-id="mixed-command"]'),
		).not.toBeNull(),
	);
	const card = log.querySelector<HTMLElement>(
		'[data-chat-activity-id="mixed-command"]',
	)!;
	const disclosure = card.querySelector<HTMLButtonElement>(
		'button[aria-expanded="false"]',
	);
	if (disclosure) fireEvent.click(disclosure);
	const output = await waitFor(() => {
		const p = card.querySelector<HTMLElement>(
			'[data-chat-command-field="command"]',
		);
		expect(p).not.toBeNull();
		return p!;
	});
	const answer = log.querySelector<HTMLElement>(
		'[data-chat-message-id="mixed-answer"] [data-chat-message-body]',
	)!;
	const range = document.createRange();
	range.setStart(output, 0);
	range.setEnd(answer, answer.childNodes.length);
	const native = window.getSelection()!;
	native.removeAllRanges();
	native.addRange(range);
	fireEvent.pointerUp(log);
	return { log, range, native, output, answer };
}
beforeEach(() => {
	vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
		new DOMRect(0, 0, 600, 500),
	);
	vi.spyOn(Range.prototype, "getBoundingClientRect").mockReturnValue(
		new DOMRect(40, 80, 200, 80),
	);
	vi.spyOn(Range.prototype, "getClientRects").mockReturnValue([
		new DOMRect(40, 80, 200, 80),
	] as unknown as DOMRectList);
	HTMLElement.prototype.scrollTo = vi.fn();
});
afterEach(() => {
	window.getSelection()?.removeAllRanges();
	vi.restoreAllMocks();
});

describe("mixed selection actions", () => {
	it("rejects a mixed range containing an unsupported steer without partially attaching", async () => {
		const snapshot = fixture();
		snapshot.items[2].sequence = 4;
		snapshot.items.splice(2, 0, {
			kind: "activity",
			id: "steer",
			turnId: "mixed-turn",
			sequence: 3,
			revision: 1,
			activityKind: "system",
			status: "completed",
			summary: "Steered",
			detail: {
				event: "steer",
				text: "Keep this instruction",
				origin: "human",
			},
			createdAt: "2026-11-01T00:00:01Z",
		});
		render(<ChatWorkspace snapshot={snapshot} excerptsEnabled />);
		await selectMixed();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Add to chat" })).toBeDisabled();
		expect(
			screen.getByText("This content cannot be attached."),
		).toBeInTheDocument();
		expect(window.getSelection()!.toString()).toContain(
			"Keep this instruction",
		);
	});

	it("reveals and highlights source segments in separate visual groups of one steered turn", async () => {
		const snapshot = fixture();
		snapshot.items.push(
			{
				kind: "activity",
				id: "steer",
				turnId: "mixed-turn",
				sequence: 4,
				revision: 1,
				activityKind: "reasoning",
				status: "completed",
				summary: "Steered",
				detail: { event: "steer", text: "Continue", origin: "human" },
				createdAt: "2026-11-01T00:00:03Z",
			},
			{
				kind: "activity",
				id: "later-command",
				turnId: "mixed-turn",
				sequence: 5,
				revision: 1,
				activityKind: "command",
				status: "completed",
				summary: "Ran command",
				detail: { command: "echo later", output: "Later output" },
				createdAt: "2026-11-01T00:00:04Z",
			},
			{
				kind: "message",
				id: "later-answer",
				turnId: "mixed-turn",
				sequence: 6,
				revision: 1,
				role: "assistant",
				origin: "provider",
				text: "Done",
				streaming: false,
				createdAt: "2026-11-01T00:00:05Z",
			},
		);
		const view = render(<ChatWorkspace snapshot={snapshot} excerptsEnabled />);
		view.rerender(
			<ChatWorkspace
				snapshot={snapshot}
				excerptsEnabled
				annotationNavigationRequest={{
					id: "jump",
					conversationId: snapshot.conversationId,
					messageId: "",
					revision: 0,
					text: "Write-Output 'Copy'\n\nLater output",
					segments: [
						{
							kind: "command",
							sourceId: "mixed-command",
							revision: 3,
							field: "command",
							text: "Write-Output 'Copy'",
						},
						{
							kind: "command",
							sourceId: "later-command",
							revision: 1,
							field: "output",
							text: "Later output",
						},
					],
				}}
			/>,
		);
		await waitFor(() =>
			expect(
				view.container.querySelectorAll(".chat-annotation-target"),
			).toHaveLength(2),
		);
		expect(
			screen.queryByText(
				"The referenced message could not be brought into view.",
			),
		).not.toBeInTheDocument();
	});

	it("attaches one annotation and forwards every source segment when sending to main", async () => {
		const send = vi.fn();
		render(
			<ChatWorkspace snapshot={fixture()} excerptsEnabled onSend={send} />,
		);
		await selectMixed();
		fireEvent.click(screen.getByRole("button", { name: "Add to chat" }));
		expect(
			screen.getByRole("button", { name: "1 annotation" }),
		).toBeInTheDocument();
		const editor = screen.getByRole("combobox", { name: "Message the agent" });
		await typeInLexicalEditor(editor, "Explain this");
		fireEvent.keyDown(editor, { key: "Enter" });
		await waitFor(() => expect(send).toHaveBeenCalledOnce());
		const refs = send.mock.calls[0][3] as ChatDraftExcerptReference[];
		expect(refs).toHaveLength(1);
		expect(refs[0].segments?.map((part) => part.kind)).toEqual([
			"command",
			"command",
			"message",
		]);
		expect(refs[0].text).toContain("Write-Output 'Copy'");
		expect(refs[0].text).toContain("Copied ✓\nDiagram");
	});

	it("keeps the side label disabled while processing and preserves a later selection", async () => {
		let finish!: () => void;
		const attach = vi.fn(
			(_excerpt: ChatDraftExcerptReference, _options?: ChatExcerptActivation) =>
				new Promise<void>((resolve) => {
					finish = resolve;
				}),
		);
		render(
			<ChatWorkspace
				snapshot={fixture()}
				excerptsEnabled
				onAddToSideChat={attach}
			/>,
		);
		const selected = await selectMixed();
		const button = screen.getByRole("button", { name: "Add to side chat" });
		fireEvent.click(button);
		fireEvent.click(button);
		expect(attach).toHaveBeenCalledOnce();
		expect(button).toBeDisabled();
		expect(button).toHaveTextContent("Add to side chat");
		const newer = document.createRange();
		newer.selectNodeContents(selected.answer);
		selected.native.removeAllRanges();
		selected.native.addRange(newer);
		fireEvent.pointerUp(selected.log);
		const options = attach.mock.calls[0][1] as {
			shouldActivate: () => boolean;
		};
		expect(options.shouldActivate()).toBe(false);
		await act(async () => finish());
		expect(selected.native.toString()).toBe("The Code result follows.");
	});

	it("retains the selection and toolbar when an attachment is rejected", async () => {
		const attach = vi.fn().mockRejectedValue(new Error("Excerpt rejected"));
		render(
			<ChatWorkspace
				snapshot={fixture()}
				excerptsEnabled
				onAddToSideChat={attach}
			/>,
		);
		const selected = await selectMixed();
		const original = selected.native.toString();
		fireEvent.click(screen.getByRole("button", { name: "Add to side chat" }));
		await screen.findByText("Excerpt rejected");
		expect(selected.native.toString()).toBe(original);
		expect(
			screen.getByRole("button", { name: "Add to side chat" }),
		).toBeEnabled();
	});

	it("ignores completion after the conversation is replaced", async () => {
		let finish!: () => void;
		const attach = vi.fn(
			(_excerpt: ChatDraftExcerptReference, _options?: ChatExcerptActivation) =>
				new Promise<void>((resolve) => {
					finish = resolve;
				}),
		);
		const view = render(
			<ChatWorkspace
				snapshot={fixture()}
				excerptsEnabled
				onAddToSideChat={attach}
			/>,
		);
		await selectMixed();
		fireEvent.click(screen.getByRole("button", { name: "Add to side chat" }));
		view.rerender(
			<ChatWorkspace
				snapshot={fixture()}
				excerptsEnabled
				onAddToSideChat={attach}
			/>,
		);
		expect(attach.mock.calls[0][1]?.isCurrent?.()).toBe(false);
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 40));
		});
		const body = screen
			.getByRole("log", { name: "Conversation" })
			.querySelector("[data-chat-message-body]")!;
		const range = document.createRange();
		range.selectNodeContents(body);
		window.getSelection()!.removeAllRanges();
		window.getSelection()!.addRange(range);
		const before = window.getSelection()!.toString();
		await act(async () => finish());
		expect(window.getSelection()!.toString()).toBe(before);
	});
});
