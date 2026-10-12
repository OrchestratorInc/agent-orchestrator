import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CONTROLLED_TEXT_INSERTION_COMMAND, HISTORY_PUSH_TAG, type LexicalEditor, REDO_COMMAND, UNDO_COMMAND } from "lexical";
import { describe, expect, it, vi } from "vitest";
import { readChatSessionDraft, writeChatComposerText } from "../../lib/chat-drafts";
import { placeLexicalCaret, typeInLexicalEditor } from "../../test/lexical";
import { TooltipProvider } from "../ui/tooltip";
import { ChatComposer } from "./ChatComposer";

function pendingSend() {
	let resolve!: () => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<void>((accept, refuse) => {
		resolve = accept;
		reject = refuse;
	});
	const onSend = vi.fn().mockReturnValueOnce(promise).mockResolvedValue(undefined);
	return { onSend, resolve, reject };
}

function renderComposer(sessionId: string, onSend: Parameters<typeof ChatComposer>[0]["onSend"], attachments = false) {
	return render(
		<TooltipProvider>
			<ChatComposer
				draftSessionId={sessionId}
				onSend={onSend}
				nativeImages={attachments}
				onStageAttachments={attachments ? vi.fn().mockResolvedValue([".ao/attachments/original.png"]) : undefined}
			/>
		</TooltipProvider>,
	);
}

async function startSend(onSend: ReturnType<typeof pendingSend>["onSend"], text: string) {
	const field = screen.getByLabelText("Message the agent");
	await typeInLexicalEditor(field, text);
	fireEvent.keyDown(field, { key: "Enter" });
	await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
	await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
	return field;
}

describe("optimistic message delivery", () => {
	it("keeps an unsaved next draft and its warning when a remounted Chat accepts the original message", async () => {
		const sessionId = "optimistic-remount-accepted-unsaved-next-draft";
		const pending = pendingSend();
		const original = renderComposer(sessionId, pending.onSend);
		await startSend(pending.onSend, "original request");
		original.unmount();
		renderComposer(sessionId, pending.onSend);
		const field = screen.getByLabelText("Message the agent");
		await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
		const storage = window.localStorage;
		const write = storage.setItem.bind(storage);
		let failWrite = true;
		const setItem = vi.spyOn(storage, "setItem").mockImplementation((key, value) => {
			if (failWrite && JSON.parse(value).composer?.text === "unsaved next draft") {
				throw new DOMException("full", "QuotaExceededError");
			}
			write(key, value);
		});
		try {
			await typeInLexicalEditor(field, "unsaved next draft");
			expect(screen.getByRole("alert")).toHaveTextContent("couldn’t be saved");

			await act(async () => pending.resolve());

			expect(field).toHaveTextContent("unsaved next draft");
			expect(screen.getByRole("alert")).toHaveTextContent("couldn’t be saved");
			expect(pending.onSend).toHaveBeenCalledOnce();
			failWrite = false;
			fireEvent.keyDown(field, { key: "Enter" });
			await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(2));
			expect(pending.onSend.mock.calls[1][0]).toBe("unsaved next draft");
			await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
			expect(field.textContent).toBe("");
			expect(readChatSessionDraft(sessionId).composer.text).toBe("");
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
			expect(pending.onSend.mock.calls.filter(([text]) => text === "original request")).toHaveLength(1);
		} finally {
			setItem.mockRestore();
		}
	});

	it("persists the next draft after storage reads recover before the original message is accepted", async () => {
		const sessionId = "optimistic-accepted-next-draft-read-failure";
		const pending = pendingSend();
		renderComposer(sessionId, pending.onSend);
		const field = await startSend(pending.onSend, "original request");
		const getItem = vi.spyOn(window.localStorage, "getItem").mockImplementation(() => {
			throw new DOMException("temporarily unreadable", "SecurityError");
		});
		try {
			await typeInLexicalEditor(field, "next draft during read failure");
			expect(screen.getByRole("alert")).toHaveTextContent("couldn’t be saved");
		} finally {
			getItem.mockRestore();
		}

		await act(async () => pending.resolve());

		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(field).toHaveTextContent("next draft during read failure");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("next draft during read failure");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(pending.onSend).toHaveBeenCalledOnce();
	});

	it("preserves a saved next draft when draft reads fail as the original message is accepted", async () => {
		const sessionId = "optimistic-accepted-saved-next-draft-read-failure";
		const draftKey = `ao.chat.draft:${encodeURIComponent(sessionId)}`;
		const pending = pendingSend();
		expect(writeChatComposerText(sessionId, "restored original request").ok).toBe(true);
		renderComposer(sessionId, pending.onSend);
		const field = screen.getByLabelText("Message the agent");
		expect(field).toHaveTextContent("restored original request");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(pending.onSend).toHaveBeenCalledOnce());
		await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
		await typeInLexicalEditor(field, "saved next draft");
		expect(field).toHaveTextContent("saved next draft");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("saved next draft");
		const read = window.localStorage.getItem.bind(window.localStorage);
		const getItem = vi.spyOn(window.localStorage, "getItem").mockImplementation((key) => {
			if (key === draftKey) throw new DOMException("temporarily unreadable", "SecurityError");
			return read(key);
		});
		try {
			await act(async () => pending.resolve());

			expect.soft(field).toHaveTextContent("saved next draft");
			expect(JSON.parse(read(draftKey)!).composer.text).toBe("saved next draft");
			expect(screen.getByRole("alert")).toHaveTextContent("acceptance couldn’t be recorded");
		} finally {
			getItem.mockRestore();
		}
		await userEvent.click(await screen.findByRole("button", { name: "Retry message safely" }));
		await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(2));
		expect(pending.onSend.mock.calls[1]).toEqual(pending.onSend.mock.calls[0]);
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(field).toHaveTextContent("saved next draft");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("saved next draft");
	});

	it.each(["selected", "dismissed"] as const)("preserves a %s skill menu when the original message is accepted", async (menuState) => {
		const sessionId = `optimistic-accepted-next-draft-skill-${menuState}`;
		const pending = pendingSend();
		render(
			<TooltipProvider>
				<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} skills={[
					{ name: "alpha", displayName: "alpha", description: "first skill", source: "user" },
					{ name: "beta", displayName: "beta", description: "second skill", source: "user" },
				]} />
			</TooltipProvider>,
		);
		const field = await startSend(pending.onSend, "original request");
		await typeInLexicalEditor(field, "/");
		fireEvent.keyDown(field, { key: menuState === "selected" ? "ArrowDown" : "Escape" });
		if (menuState === "selected") {
			expect(screen.getByRole("option", { name: /\/beta/ })).toHaveAttribute("aria-selected", "true");
		} else {
			expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
		}

		await act(async () => pending.resolve());

		if (menuState === "selected") {
			expect.soft(screen.getByRole("option", { name: /\/beta/ })).toHaveAttribute("aria-selected", "true");
			await userEvent.keyboard("{Enter}");
			expect(field.querySelector('[data-composer-token="skill"]')).toHaveTextContent("/beta");
			expect(pending.onSend).toHaveBeenCalledOnce();
		} else {
			expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
			await userEvent.keyboard("{Tab}");
			expect(field.textContent).toBe("/");
			expect(pending.onSend).toHaveBeenCalledOnce();
		}
	});

	it("preserves the next draft caret and undo history when the original message is accepted", async () => {
		const sessionId = "optimistic-accepted-next-draft-caret";
		const pending = pendingSend();
		renderComposer(sessionId, pending.onSend);
		const field = await startSend(pending.onSend, "original request");
		await typeInLexicalEditor(field, "draft B");
		await placeLexicalCaret(field, 2);
		const editor = (field as HTMLElement & { __lexicalEditor: LexicalEditor }).__lexicalEditor;

		await act(async () => pending.resolve());
		await act(async () => {
			editor.update(() => {
				editor.dispatchCommand(CONTROLLED_TEXT_INSERTION_COMMAND, "Z");
			}, { discrete: true, tag: HISTORY_PUSH_TAG });
		});

		expect(field).toHaveTextContent("drZaft B");
		await act(async () => { editor.dispatchCommand(UNDO_COMMAND, undefined); });
		expect(field).toHaveTextContent("draft B");
		await act(async () => { editor.dispatchCommand(UNDO_COMMAND, undefined); });
		expect(field.textContent).toBe("");
		expect(pending.onSend).toHaveBeenCalledOnce();
	});

	it("keeps redo history for a next draft undone to empty before the original message is accepted", async () => {
		const sessionId = "optimistic-accepted-empty-next-draft-history";
		const pending = pendingSend();
		renderComposer(sessionId, pending.onSend);
		const field = await startSend(pending.onSend, "original request");
		await typeInLexicalEditor(field, "draft B");
		const editor = (field as HTMLElement & { __lexicalEditor: LexicalEditor }).__lexicalEditor;
		await act(async () => { editor.dispatchCommand(UNDO_COMMAND, undefined); });
		expect(field.textContent).toBe("");

		await act(async () => pending.resolve());
		await act(async () => { editor.dispatchCommand(REDO_COMMAND, undefined); });

		expect(field).toHaveTextContent("draft B");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("draft B");
		expect(pending.onSend).toHaveBeenCalledOnce();
	});

	it("restores a refused message after Chat remounts without a next draft", async () => {
		const sessionId = "optimistic-remount-wake-refused";
		const pending = pendingSend();
		const original = renderComposer(sessionId, pending.onSend);
		await startSend(pending.onSend, "message to restore");
		original.unmount();
		renderComposer(sessionId, pending.onSend);
		const replacement = screen.getByLabelText("Message the agent");
		expect(replacement.textContent).toBe("");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();

		await act(async () => pending.reject({ code: "CHAT_RESUME_FAILED", message: "Could not resume" }));

		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(readChatSessionDraft(sessionId).composer.text).toBe("message to restore");
		expect(replacement).toHaveTextContent("message to restore");
		expect(replacement).toHaveAttribute("contenteditable", "true");
	});

	it.each([
		["response lost", new Error("response lost")],
		["resume refused", { code: "CHAT_RESUME_FAILED", message: "Could not resume" }],
	])("retries the original message and image without changing the next draft (%s)", async (reason, error) => {
		const sessionId = `optimistic-rejected-image-request-${reason}`;
		const pending = pendingSend();
		renderComposer(sessionId, pending.onSend, true);
		const field = screen.getByLabelText("Message the agent");
		fireEvent.paste(field, {
			clipboardData: {
				files: [new File([new Uint8Array([137, 80, 78, 71])], "original.png", { type: "image/png" })],
				items: [],
				getData: () => "",
			},
		});
		await screen.findByLabelText("Remove original.png");
		await startSend(pending.onSend, "inspect this image");
		await typeInLexicalEditor(field, "write a README");

		await act(async () => pending.reject(error));

		expect(field).toHaveTextContent("write a README");
		if (error instanceof Error) expect(screen.getByRole("alert")).toHaveTextContent("Message delivery wasn’t confirmed");
		await userEvent.click(await screen.findByRole("button", { name: "Retry message safely" }));
		await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(2));
		expect(pending.onSend.mock.calls[1]).toEqual(pending.onSend.mock.calls[0]);
		expect(pending.onSend.mock.calls[1][1]).toEqual([{ mimeType: "image/png", data: "iVBORw==" }]);
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(field).toHaveTextContent("write a README");
		expect(screen.queryByLabelText("Remove original.png")).not.toBeInTheDocument();
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(3));
		expect(pending.onSend.mock.calls[2][0]).toBe("write a README");
		expect(pending.onSend.mock.calls[2][1]).toBeUndefined();
	});

	it("keeps both drafts after a rejected send when the next draft could not be saved", async () => {
		const sessionId = "optimistic-rejected-unsaved-next-draft";
		const pending = pendingSend();
		renderComposer(sessionId, pending.onSend);
		const field = await startSend(pending.onSend, "original request");
		const storage = window.localStorage;
		let failWrite = true;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue({
			getItem: storage.getItem.bind(storage),
			removeItem: storage.removeItem.bind(storage),
			setItem: (key: string, value: string) => {
				if (failWrite && JSON.parse(value).composer?.text === "unsaved next draft") {
					throw new DOMException("full", "QuotaExceededError");
				}
				storage.setItem(key, value);
			},
		} as Storage);
		try {
			await typeInLexicalEditor(field, "unsaved next draft");
			await act(async () => pending.reject({ code: "CHAT_RESUME_FAILED", message: "Could not resume" }));
			expect(field).toHaveTextContent("unsaved next draft");
			failWrite = false;
			await userEvent.click(await screen.findByRole("button", { name: "Retry message safely" }));
			await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(2));
			expect(pending.onSend.mock.calls[1]).toEqual(pending.onSend.mock.calls[0]);
			await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
			expect(field).toHaveTextContent("unsaved next draft");
			expect(readChatSessionDraft(sessionId).composer.text).toBe("unsaved next draft");
		} finally {
			localStorage.mockRestore();
		}
	});
});
