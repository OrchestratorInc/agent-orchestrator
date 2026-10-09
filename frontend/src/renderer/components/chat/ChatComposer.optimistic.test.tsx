import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CONTROLLED_TEXT_INSERTION_COMMAND, HISTORY_PUSH_TAG, type LexicalEditor, REDO_COMMAND, UNDO_COMMAND } from "lexical";
import { describe, expect, it, vi } from "vitest";
import { readChatSessionDraft, writeChatAttachments, writeChatComposerText } from "../../lib/chat-drafts";
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
	it("keeps the memory-only fallback locked until a rejected message is restored", async () => {
		const pending = pendingSend();
		render(<TooltipProvider><ChatComposer onSend={pending.onSend} /></TooltipProvider>);
		const field = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(field, "memory-only request");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(pending.onSend).toHaveBeenCalledOnce());
		expect(field.textContent).toBe("");
		expect(field).toHaveAttribute("contenteditable", "false");

		await act(async () => pending.reject({ code: "CHAT_RESUME_FAILED", message: "Could not resume" }));

		expect(field).toHaveTextContent("memory-only request");
		expect(field).toHaveAttribute("contenteditable", "true");
		expect(screen.getByRole("alert")).toHaveTextContent("Your draft was kept");
	});

	it("preserves a next draft with the same text as the pending message", async () => {
		const sessionId = "optimistic-identical-next-draft";
		const pending = pendingSend();
		renderComposer(sessionId, pending.onSend);
		const field = await startSend(pending.onSend, "x");
		await typeInLexicalEditor(field, "x");

		await act(async () => pending.resolve());

		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(field).toHaveTextContent("x");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("x");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(2));
		expect(pending.onSend.mock.calls[1][0]).toBe("x");
		expect(pending.onSend.mock.calls[1][2]).not.toBe(pending.onSend.mock.calls[0][2]);
	});

	it("does not overwrite a newer restored draft when the old surface accepts its message", async () => {
		const sessionId = "optimistic-newer-restored-draft";
		const pending = pendingSend();
		const original = renderComposer(sessionId, pending.onSend);
		const originalField = await startSend(pending.onSend, "original message");
		await typeInLexicalEditor(originalField, "draft before leaving");
		original.unmount();
		writeChatComposerText(sessionId, "newer draft from replacement");

		await act(async () => pending.resolve());

		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(readChatSessionDraft(sessionId).composer.text).toBe("newer draft from replacement");
		renderComposer(sessionId, pending.onSend);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("newer draft from replacement");
	});

	it("keeps the composer clear and editable when Chat remounts during delivery", async () => {
		const sessionId = "optimistic-remount-in-flight";
		const pending = pendingSend();
		const original = renderComposer(sessionId, pending.onSend);
		await startSend(pending.onSend, "pending message");
		original.unmount();
		renderComposer(sessionId, pending.onSend);
		const replacement = screen.getByLabelText("Message the agent");
		await waitFor(() => expect(replacement).toHaveAttribute("contenteditable", "true"));
		expect(replacement.textContent).toBe("");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		await typeInLexicalEditor(replacement, "next draft after returning");

		await act(async () => pending.resolve());

		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(replacement).toHaveTextContent("next draft after returning");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("next draft after returning");
		expect(pending.onSend).toHaveBeenCalledOnce();
	});

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

	it("retries a rejected original message and image without replacing or attaching them to the next draft", async () => {
		const sessionId = "optimistic-rejected-image-request";
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

		await act(async () => pending.reject({ code: "CHAT_RESUME_FAILED", message: "Could not resume" }));

		expect(field).toHaveTextContent("write a README");
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

	it("preserves typing while a restored image is read before dispatch", async () => {
		const sessionId = "optimistic-restored-image-read";
		writeChatComposerText(sessionId, "inspect restored image");
		writeChatAttachments(sessionId, [{
			id: "restored-image",
			name: "restored.png",
			mimeType: "image/png",
			bytes: 4,
			path: ".ao/attachments/restored.png",
		}]);
		let resolveRead!: (response: Response) => void;
		const read = new Promise<Response>((resolve) => { resolveRead = resolve; });
		const fetch = vi.spyOn(globalThis, "fetch").mockReturnValue(read);
		const pending = pendingSend();
		const view = renderComposer(sessionId, pending.onSend, true);
		try {
			const field = screen.getByLabelText("Message the agent");
			fireEvent.keyDown(field, { key: "Enter" });
			await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
			expect(pending.onSend).not.toHaveBeenCalled();
			expect(field.textContent).toBe("");
			await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
			await typeInLexicalEditor(field, "next draft during image read");

			const response = new Response();
			vi.spyOn(response, "blob").mockResolvedValue(new Blob([new Uint8Array([137, 80, 78, 71])], { type: "image/png" }));
			await act(async () => resolveRead(response));

			await waitFor(() => expect(pending.onSend).toHaveBeenCalledOnce());
			expect(pending.onSend).toHaveBeenCalledWith(
				"inspect restored image\n\nAttached files (read these files in the workspace):\n- .ao/attachments/restored.png",
				[{ mimeType: "image/png", data: "iVBORw==" }],
				expect.any(String),
			);
			expect(field).toHaveTextContent("next draft during image read");
			await act(async () => pending.resolve());
			await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
			expect(field).toHaveTextContent("next draft during image read");
			expect(readChatSessionDraft(sessionId).composer.text).toBe("next draft during image read");
		} finally {
			view.unmount();
			fetch.mockRestore();
		}
	});
});
