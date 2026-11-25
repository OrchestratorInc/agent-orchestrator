import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Activity } from "react";
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

	it("sends a message entered while the previous one is still being sent, right after it", async () => {
		const sessionId = "optimistic-rapid-next-message";
		const pending = pendingSend();
		renderComposer(sessionId, pending.onSend);
		const field = await startSend(pending.onSend, "first message");
		await typeInLexicalEditor(field, "second message");
		fireEvent.keyDown(field, { key: "Enter" });

		expect(pending.onSend).toHaveBeenCalledOnce();
		expect(field).toHaveTextContent("second message");

		await act(async () => pending.resolve());

		await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(2));
		expect(pending.onSend.mock.calls.map(([text]) => text)).toEqual(["first message", "second message"]);
		expect(pending.onSend.mock.calls[1][2]).toEqual(expect.any(String));
		expect(pending.onSend.mock.calls[1][2]).not.toBe(pending.onSend.mock.calls[0][2]);
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(field.textContent).toBe("");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("takes a file for the next message during a send and keeps it when that send lands first", async () => {
		const sessionId = "optimistic-next-message-file";
		const pending = pendingSend();
		let finishStaging!: (paths: string[]) => void;
		const stage = vi.fn(() => new Promise<string[]>((resolve) => { finishStaging = resolve; }));
		render(
			<TooltipProvider>
				<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} onStageAttachments={stage} restoring />
			</TooltipProvider>,
		);
		const field = await startSend(pending.onSend, "first message");
		expect(screen.getByRole("button", { name: "Attach a file" })).toBeEnabled();
		const next = new File([new Uint8Array([137, 80, 78, 71])], "next.png", { type: "image/png" });
		fireEvent.paste(field, { clipboardData: { files: [next], items: [], getData: () => "", setData: () => undefined } });
		await waitFor(() => expect(stage).toHaveBeenCalledOnce());

		await act(async () => pending.resolve());
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		await act(async () => finishStaging([".ao/attachments/next.png"]));

		await screen.findByLabelText("Remove next.png");
		expect(readChatSessionDraft(sessionId).composer.attachments.map((attachment) => attachment.path)).toEqual([".ao/attachments/next.png"]);
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(2));
		expect(pending.onSend.mock.calls[1][0]).toContain(".ao/attachments/next.png");
	});

	function pasteImages(field: HTMLElement, ...names: string[]) {
		fireEvent.paste(field, {
			clipboardData: {
				files: names.map((name) => new File([new Uint8Array([137, 80, 78, 71])], name, { type: "image/png" })),
				items: [],
				getData: () => "",
				setData: () => undefined,
			},
		});
	}

	function stagerHeldAfterFirst() {
		let finish!: (paths: string[]) => void;
		let fail!: (error: unknown) => void;
		const stage = vi.fn((files: unknown[]) => stage.mock.calls.length === 1
			? Promise.resolve(files.map((_, index) => `.ao/attachments/sent-${index}.png`))
			: new Promise<string[]>((resolve, reject) => { finish = resolve; fail = reject; }));
		return { stage, finish: (paths: string[]) => finish(paths), fail: (error: unknown) => fail(error) };
	}

	it("does not send a file again after its chat was hidden while the send landed", async () => {
		const sessionId = "optimistic-sent-file-once";
		const pending = pendingSend();
		const held = stagerHeldAfterFirst();
		const chat = (mode: "visible" | "hidden") => (
			<TooltipProvider>
				<Activity mode={mode}>
					<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} onStageAttachments={held.stage} restoring />
				</Activity>
			</TooltipProvider>
		);
		const view = render(chat("visible"));
		pasteImages(screen.getByLabelText("Message the agent"), "sent.png");
		await screen.findByLabelText("Remove sent.png");
		const field = await startSend(pending.onSend, "first message");
		pasteImages(field, "next.png");
		await waitFor(() => expect(held.stage).toHaveBeenCalledTimes(2));

		view.rerender(chat("hidden"));
		await act(async () => pending.resolve());
		await act(async () => held.finish([".ao/attachments/next.png"]));
		view.rerender(chat("visible"));

		await screen.findByLabelText("Remove next.png");
		expect(screen.queryByLabelText("Remove sent.png")).not.toBeInTheDocument();
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.attachments.map((attachment) => attachment.path)).toEqual([".ao/attachments/next.png"]));
		fireEvent.keyDown(screen.getByLabelText("Message the agent"), { key: "Enter" });
		await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(2));
		expect(pending.onSend.mock.calls[1][0]).toContain(".ao/attachments/next.png");
		expect(pending.onSend.mock.calls[1][0]).not.toContain("sent-0.png");
	});

	it("keeps a next file a remounted chat took when the first chat's send lands", async () => {
		const sessionId = "optimistic-remount-next-file";
		const pending = pendingSend();
		const stage = vi.fn(async (files: unknown[]) => files.map(() => `.ao/attachments/${stage.mock.calls.length}.png`));
		const chat = () => (
			<TooltipProvider>
				<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} onStageAttachments={stage} restoring />
			</TooltipProvider>
		);
		const first = render(chat());
		pasteImages(screen.getByLabelText("Message the agent"), "sent.png");
		await screen.findByLabelText("Remove sent.png");
		await startSend(pending.onSend, "first message");
		first.unmount();
		render(chat());
		pasteImages(screen.getByLabelText("Message the agent"), "next.png");
		await screen.findByLabelText("Remove next.png");

		await act(async () => pending.resolve());

		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(screen.getByLabelText("Remove next.png")).toBeInTheDocument();
		expect(screen.queryByLabelText("Remove sent.png")).not.toBeInTheDocument();
		expect(readChatSessionDraft(sessionId).composer.attachments.map((attachment) => attachment.name)).toEqual(["next.png"]);
	});

	it("gives the next message its own file limit during a send and lets its files be removed", async () => {
		const sessionId = "optimistic-next-message-limit";
		const pending = pendingSend();
		const stage = vi.fn(async (files: unknown[]) => files.map((_, index) => `.ao/attachments/${stage.mock.calls.length}-${index}.png`));
		render(
			<TooltipProvider>
				<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} onStageAttachments={stage} restoring />
			</TooltipProvider>,
		);
		pasteImages(screen.getByLabelText("Message the agent"), ...Array.from({ length: 8 }, (_, index) => `sent-${index}.png`));
		await screen.findByLabelText("Remove sent-7.png");
		const field = await startSend(pending.onSend, "first message");

		pasteImages(field, "next.png");

		const remove = await screen.findByLabelText("Remove next.png");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		await userEvent.click(remove);
		expect(screen.queryByLabelText("Remove next.png")).not.toBeInTheDocument();
		expect(readChatSessionDraft(sessionId).composer.attachments).toEqual([]);
		await act(async () => pending.resolve());
	});

	it("sends a file entered on its own during a send right after that send", async () => {
		const sessionId = "optimistic-next-file-only-enter";
		const pending = pendingSend();
		const stage = vi.fn().mockResolvedValue([".ao/attachments/next.png"]);
		render(
			<TooltipProvider>
				<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} onStageAttachments={stage} restoring />
			</TooltipProvider>,
		);
		const field = await startSend(pending.onSend, "first message");
		pasteImages(field, "next.png");
		await screen.findByLabelText("Remove next.png");
		fireEvent.keyDown(field, { key: "Enter" });
		expect(pending.onSend).toHaveBeenCalledOnce();

		await act(async () => pending.resolve());

		await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(2));
		expect(pending.onSend.mock.calls[1][0]).toContain(".ao/attachments/next.png");
	});

	it("keeps a refused message beside the box when only a file was taken for the next one", async () => {
		const sessionId = "optimistic-refused-next-file-only";
		const pending = pendingSend();
		const stage = vi.fn(async (files: unknown[]) => files.map(() => `.ao/attachments/${stage.mock.calls.length}.png`));
		render(
			<TooltipProvider>
				<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} onStageAttachments={stage} restoring />
			</TooltipProvider>,
		);
		pasteImages(screen.getByLabelText("Message the agent"), "sent.png");
		await screen.findByLabelText("Remove sent.png");
		const field = await startSend(pending.onSend, "first message");
		pasteImages(field, "next.png");
		await screen.findByLabelText("Remove next.png");

		await act(async () => pending.reject({ code: "CHAT_RESUME_FAILED", message: "Could not resume" }));

		expect(await screen.findByTestId("unsent-message-strip")).toHaveTextContent("first message");
		expect(field).toHaveAttribute("contenteditable", "true");
		expect(field).not.toHaveTextContent("first message");
		expect(screen.getByLabelText("Remove next.png")).toBeInTheDocument();
		expect(screen.queryByLabelText("Remove sent.png")).not.toBeInTheDocument();
	});

	it("says why a restore failed rather than that its file was not attached", async () => {
		const sessionId = "optimistic-restore-failed-reason";
		const pending = pendingSend();
		const held = stagerHeldAfterFirst();
		render(
			<TooltipProvider>
				<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} onStageAttachments={held.stage} restoring />
			</TooltipProvider>,
		);
		pasteImages(screen.getByLabelText("Message the agent"), "sent.png");
		await screen.findByLabelText("Remove sent.png");
		const field = await startSend(pending.onSend, "first message");
		pasteImages(field, "next.png");
		await waitFor(() => expect(held.stage).toHaveBeenCalledTimes(2));
		const refusal = { code: "SESSION_RESTORE_FAILED", message: "Its branch is gone" };

		await act(async () => {
			held.fail(refusal);
			pending.reject(refusal);
		});

		expect(await screen.findByRole("alert")).toHaveTextContent("Its branch is gone");
	});

	it("takes a second file while the first still waits for the restore", async () => {
		const sessionId = "optimistic-second-file-waits";
		const pending = pendingSend();
		const finish: ((paths: string[]) => void)[] = [];
		const stage = vi.fn(() => new Promise<string[]>((resolve) => { finish.push(resolve); }));
		render(
			<TooltipProvider>
				<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} onStageAttachments={stage} restoring />
			</TooltipProvider>,
		);
		const field = await startSend(pending.onSend, "first message");
		pasteImages(field, "one.png");
		await screen.findByLabelText("Attaching one.png");
		pasteImages(field, "two.png");
		await screen.findByLabelText("Attaching two.png");

		await act(async () => finish[0]([".ao/attachments/one.png"]));
		await waitFor(() => expect(stage).toHaveBeenCalledTimes(2));
		await act(async () => finish[1]([".ao/attachments/two.png"]));

		await screen.findByLabelText("Remove one.png");
		await screen.findByLabelText("Remove two.png");
		await act(async () => pending.resolve());
	});

	it("does not send a held message whose files changed after its Enter", async () => {
		const sessionId = "optimistic-held-files-changed";
		const pending = pendingSend();
		const stage = vi.fn(async (files: unknown[]) => files.map(() => `.ao/attachments/${stage.mock.calls.length}.png`));
		render(
			<TooltipProvider>
				<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} onStageAttachments={stage} restoring />
			</TooltipProvider>,
		);
		const field = await startSend(pending.onSend, "first message");
		pasteImages(field, "wrong.png");
		const wrong = await screen.findByLabelText("Remove wrong.png");
		fireEvent.keyDown(field, { key: "Enter" });
		await userEvent.click(wrong);
		pasteImages(field, "right.png");
		await screen.findByLabelText("Remove right.png");

		await act(async () => pending.resolve());

		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		await act(async () => {});
		expect(pending.onSend).toHaveBeenCalledOnce();
		expect(screen.getByLabelText("Remove right.png")).toBeInTheDocument();
	});

	it("takes no file during a send that restores nothing", async () => {
		const sessionId = "optimistic-ordinary-send-no-files";
		const pending = pendingSend();
		const stage = vi.fn().mockResolvedValue([".ao/attachments/next.png"]);
		render(
			<TooltipProvider>
				<ChatComposer draftSessionId={sessionId} onSend={pending.onSend} onStageAttachments={stage} />
			</TooltipProvider>,
		);
		const field = await startSend(pending.onSend, "first message");
		expect(screen.getByRole("button", { name: "Attach a file" })).toBeDisabled();
		pasteImages(field, "next.png");
		await act(async () => pending.resolve());
		expect(stage).not.toHaveBeenCalled();
	});

	it("keeps a sent file that could not be staged out of the strip during its send", async () => {
		const pending = pendingSend();
		render(
			<TooltipProvider>
				<ChatComposer draftSessionId="optimistic-held-file-strip" onSend={pending.onSend} holdAttachments />
			</TooltipProvider>,
		);
		pasteImages(screen.getByLabelText("Message the agent"), "held.png");
		await screen.findByLabelText("Remove held.png");
		await startSend(pending.onSend, "brief");
		expect(screen.queryByLabelText("Remove held.png")).not.toBeInTheDocument();
		await act(async () => pending.resolve());
	});

	it("sends nothing extra when Enter is pressed again on the cleared box during a send", async () => {
		const sessionId = "optimistic-repeat-enter";
		const pending = pendingSend();
		renderComposer(sessionId, pending.onSend);
		const field = await startSend(pending.onSend, "only once");
		fireEvent.keyDown(field, { key: "Enter" });
		fireEvent.keyDown(field, { key: "Enter" });

		await act(async () => pending.resolve());

		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		await act(async () => {});
		expect(pending.onSend).toHaveBeenCalledOnce();
		expect(field.textContent).toBe("");
	});

	it("keeps a message entered during a send that got no answer in the box instead of sending it first", async () => {
		const sessionId = "optimistic-rapid-next-message-lost";
		const pending = pendingSend();
		renderComposer(sessionId, pending.onSend);
		const field = await startSend(pending.onSend, "first message");
		await typeInLexicalEditor(field, "second message");
		fireEvent.keyDown(field, { key: "Enter" });

		await act(async () => pending.reject(new Error("response lost")));

		expect(await screen.findByTestId("unsent-message-strip")).toHaveTextContent("first message");
		await act(async () => {});
		expect(pending.onSend).toHaveBeenCalledOnce();
		expect(field).toHaveTextContent("second message");
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
			expect(screen.getByRole("option", { name: /^Beta/ })).toHaveAttribute("aria-selected", "true");
		} else {
			expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
		}

		await act(async () => pending.resolve());

		if (menuState === "selected") {
			expect.soft(screen.getByRole("option", { name: /^Beta/ })).toHaveAttribute("aria-selected", "true");
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
		expect(field).toHaveAttribute("contenteditable", "true");
		expect(await screen.findByTestId("unsent-message-strip")).toHaveTextContent("inspect this image");
		// The parked image leaves memory with the next draft, so Retry re-reads its staged file.
		const response = new Response();
		vi.spyOn(response, "blob").mockResolvedValue(new Blob([new Uint8Array([137, 80, 78, 71])], { type: "image/png" }));
		const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(response);
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(pending.onSend).toHaveBeenCalledTimes(2));
		fetch.mockRestore();
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
			await userEvent.click(await screen.findByRole("button", { name: "Retry" }));
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
