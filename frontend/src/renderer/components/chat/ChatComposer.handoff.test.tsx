import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { handOffComposerDraft, takeComposerHandoff } from "../../lib/chat-context-bus";
import { readChatSessionDraft, writeChatComposerText } from "../../lib/chat-drafts";
import { typeInLexicalEditor } from "../../test/lexical";
import { TooltipProvider } from "../ui/tooltip";
import { ChatComposer, type ChatComposerHandle } from "./ChatComposer";

type ComposerProps = Parameters<typeof ChatComposer>[0];

function renderComposer(props: ComposerProps) {
	const view = render(<TooltipProvider><ChatComposer {...props} /></TooltipProvider>);
	return {
		...view,
		update: (next: Partial<ComposerProps>) => view.rerender(<TooltipProvider><ChatComposer {...props} {...next} /></TooltipProvider>),
	};
}

// The new chat page: its brief's send runs as long as the session takes to
// create, and the page reads as busy from the send until it hands over.
async function startBrief(sessionId: string) {
	let resolve!: () => void;
	const onSend = vi.fn().mockReturnValueOnce(new Promise<void>((done) => { resolve = done; })).mockResolvedValue(undefined);
	const ref = createRef<ChatComposerHandle>();
	const view = renderComposer({ draftSessionId: sessionId, onSend, focusRef: ref, parkUnsentSends: false });
	const field = screen.getByLabelText("Message the agent");
	await typeInLexicalEditor(field, "Fix the build");
	fireEvent.keyDown(field, { key: "Enter" });
	await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
	view.update({ busy: true });
	await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
	return { field, onSend, ref, resolve: () => act(async () => resolve()) };
}

describe("handing a follow-up from the new chat page to its session", () => {
	it("takes a follow-up entered while the brief starts, to be sent, and clears it here", async () => {
		const sessionId = "new-chat:handoff-entered";
		const brief = await startBrief(sessionId);
		await typeInLexicalEditor(brief.field, "Also add tests");
		fireEvent.keyDown(brief.field, { key: "Enter" });
		await brief.resolve();

		// The page stays busy until it leaves, so nothing starts a second session.
		expect(brief.onSend).toHaveBeenCalledOnce();
		expect(brief.ref.current?.takeDraft()).toEqual({ text: "Also add tests", send: true });
		expect(readChatSessionDraft(sessionId).composer.text).toBe("");
	});

	it("takes a follow-up still being typed as a draft only", async () => {
		const sessionId = "new-chat:handoff-typing";
		const brief = await startBrief(sessionId);
		await typeInLexicalEditor(brief.field, "Also add");
		await brief.resolve();

		expect(brief.ref.current?.takeDraft()).toEqual({ text: "Also add", send: false });
	});

	it("does not send a follow-up edited after Enter", async () => {
		const sessionId = "new-chat:handoff-edited";
		const brief = await startBrief(sessionId);
		await typeInLexicalEditor(brief.field, "Also add tests");
		fireEvent.keyDown(brief.field, { key: "Enter" });
		await typeInLexicalEditor(brief.field, " please");
		await brief.resolve();

		expect(brief.ref.current?.takeDraft()).toEqual({ text: "Also add tests please", send: false });
	});

	it("takes nothing from an empty box", async () => {
		const brief = await startBrief("new-chat:handoff-empty");
		await brief.resolve();

		expect(brief.ref.current?.takeDraft()).toBeUndefined();
	});

	it("starts the session's composer with an entered follow-up and sends it once", async () => {
		const sessionId = "handoff-session-send";
		handOffComposerDraft(sessionId, { text: "Also add tests", send: true });
		const onSend = vi.fn().mockResolvedValue(undefined);
		renderComposer({ draftSessionId: sessionId, onSend, willQueue: true });

		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		expect(onSend.mock.calls[0][0]).toBe("Also add tests");
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(readChatSessionDraft(sessionId).composer.text).toBe("");
		expect(takeComposerHandoff(sessionId)).toBeUndefined();
	});

	it("waits for the session's composer to open before sending the follow-up", async () => {
		const sessionId = "handoff-session-wait";
		handOffComposerDraft(sessionId, { text: "Also add tests", send: true });
		const onSend = vi.fn().mockResolvedValue(undefined);
		const view = renderComposer({ draftSessionId: sessionId, onSend, disabled: true });
		await act(async () => {});
		expect(onSend).not.toHaveBeenCalled();
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("Also add tests");

		view.update({ disabled: false, willQueue: true });

		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		expect(onSend.mock.calls[0][0]).toBe("Also add tests");
	});

	it("leaves a follow-up that was still being typed in the session's box", async () => {
		const sessionId = "handoff-session-draft";
		handOffComposerDraft(sessionId, { text: "Also add", send: false });
		const onSend = vi.fn().mockResolvedValue(undefined);
		renderComposer({ draftSessionId: sessionId, onSend });

		await waitFor(() => expect(screen.getByLabelText("Message the agent")).toHaveTextContent("Also add"));
		await act(async () => {});
		expect(onSend).not.toHaveBeenCalled();
		expect(readChatSessionDraft(sessionId).composer.text).toBe("Also add");
	});

	it("keeps a follow-up whose save fails in the box, unsent, until the person sends it", async () => {
		const sessionId = "handoff-session-save-fails";
		handOffComposerDraft(sessionId, { text: "Also add tests", send: true });
		const storage = window.localStorage;
		const write = storage.setItem.bind(storage);
		let failWrite = true;
		const composerText = (value: string) => {
			try {
				return JSON.parse(value).composer?.text;
			} catch {
				return undefined;
			}
		};
		const setItem = vi.spyOn(storage, "setItem").mockImplementation((key, value) => {
			if (failWrite && composerText(value) === "Also add tests") throw new DOMException("full", "QuotaExceededError");
			write(key, value);
		});
		try {
			const onSend = vi.fn().mockResolvedValue(undefined);
			renderComposer({ draftSessionId: sessionId, onSend, willQueue: true });
			const field = screen.getByLabelText("Message the agent");

			await waitFor(() => expect(field).toHaveTextContent("Also add tests"));
			expect(screen.getByRole("alert")).toHaveTextContent("couldn’t be saved");
			await act(async () => {});
			expect(onSend).not.toHaveBeenCalled();

			failWrite = false;
			fireEvent.keyDown(field, { key: "Enter" });
			await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
			expect(onSend.mock.calls[0][0]).toBe("Also add tests");
		} finally {
			setItem.mockRestore();
		}
	});

	it("shows a follow-up as unsaved typing in a composer that cannot save drafts, for the person to send", async () => {
		const sessionId = "handoff-session-memory";
		handOffComposerDraft(sessionId, { text: "Also add tests", send: true });
		const onSend = vi.fn().mockResolvedValue(undefined);
		renderComposer({ handoffSessionId: sessionId, onSend, willQueue: true });
		const field = screen.getByLabelText("Message the agent");

		await waitFor(() => expect(field).toHaveTextContent("Also add tests"));
		await act(async () => {});
		expect(onSend).not.toHaveBeenCalled();
		expect(takeComposerHandoff(sessionId)).toBeUndefined();

		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		expect(onSend.mock.calls[0][0]).toBe("Also add tests");
	});

	it("puts a follow-up below the session's own draft, for the person to send", async () => {
		const sessionId = "handoff-session-own-draft";
		writeChatComposerText(sessionId, "My own words");
		handOffComposerDraft(sessionId, { text: "Also add tests", send: true });
		const onSend = vi.fn().mockResolvedValue(undefined);
		renderComposer({ draftSessionId: sessionId, onSend });

		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.text).toBe("My own words\n\nAlso add tests"));
		await act(async () => {});
		expect(onSend).not.toHaveBeenCalled();
	});
});
