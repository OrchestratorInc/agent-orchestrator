import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "../ui/tooltip";
import { lexicalEditorText, typeInLexicalEditor } from "../../test/lexical";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useIndependentSideChats } from "./IndependentSideChats";

const { get, post, put, remove, patch } = vi.hoisted(() => ({
	get: vi.fn(),
	post: vi.fn(),
	put: vi.fn(),
	remove: vi.fn(),
	patch: vi.fn(),
}));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, POST: post, PUT: put, DELETE: remove, PATCH: patch },
	apiErrorMessage: () => "Send failed",
	getApiBaseUrl: () => "",
	subscribeApiBaseUrl: () => () => {},
}));
vi.mock("../../lib/bridge", () => ({
	aoBridge: { sideChats: { capture: async () => {} } },
}));

describe("side delivery draft ownership", () => {
	let accept: (value: { error?: object }) => void;
	let reject: (error: Error) => void;
	beforeEach(() => {
		vi.clearAllMocks();
		get.mockImplementation(async (path: string) => {
			if (path.endsWith("/side-chats"))
				return {
					data: {
						sides: [{ id: "side", sessionId: "session", state: "ready" }],
					},
				};
			if (path.endsWith("/draft")) return { data: { contentJson: "" } };
			return {
				data: {
					snapshot: {
						side: { id: "side", state: "ready" },
						messages: [],
						turns: [],
						activities: [],
					},
				},
			};
		});
		post.mockImplementation(
			() =>
				new Promise((resolve, fail) => {
					accept = resolve;
					reject = fail;
				}),
		);
		put.mockResolvedValue({});
		remove.mockResolvedValue({});
	});

	async function beginSend(text = "first") {
		const hook = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
		await waitFor(() => expect(hook.result.current.activeId).toBe("side"));
		act(() => hook.result.current.setQuestionDraft("side", text));
		let sending!: Promise<void>;
		act(() => {
			sending = hook.result.current.send("side", text);
		});
		await waitFor(() => expect(post).toHaveBeenCalledOnce());
		return { ...hook, sending };
	}

	it("preserves a reference added while acceptance is pending", async () => {
		const hook = await beginSend();
		act(() =>
			hook.result.current.addReference("side", {
				id: "chip",
				conversationId: "main",
				messageId: "message",
				revision: 1,
				text: "sun",
				role: "assistant",
			}),
		);
		await act(async () => {
			accept({});
			await hook.sending;
		});
		await waitFor(() =>
			expect(
				put.mock.calls.some(([, options]) => {
					const draft = JSON.parse(options.body.contentJson);
					return draft.text === "first" && draft.references[0]?.text === "sun";
				}),
			).toBe(true),
		);
	});

	it("clears only an unchanged submitted draft", async () => {
		const hook = await beginSend();
		await act(async () => {
			accept({});
			await hook.sending;
		});
		await waitFor(() =>
			expect(put.mock.calls.some(([, options]) => JSON.parse(options.body.contentJson).text === "")).toBe(true),
		);
	});

	it("keeps the draft on failure", async () => {
		const hook = await beginSend();
		await act(async () => {
			accept({ error: {} });
			await hook.sending;
		});
		await waitFor(() =>
			expect(put.mock.calls.some(([, options]) => JSON.parse(options.body.contentJson).text === "first")).toBe(true),
		);
	});

	it("clears a submitted /btw question without sending the command", async () => {
		const hook = await beginSend("/btw first");
		expect(post.mock.calls[0][1].body.text).toBe("first");
		await act(async () => {
			accept({});
			await hook.sending;
		});
		await waitFor(() =>
			expect(put.mock.calls.some(([, options]) => JSON.parse(options.body.contentJson).text === "")).toBe(true),
		);
	});
	it("reuses an uncertain receipt and preserves a newer draft", async () => {
		const hook = await beginSend("original");
		const original = post.mock.calls[0][1].body;
		await act(async () => {
			reject(new Error("response lost"));
			await hook.sending;
		});
		act(() => hook.result.current.setQuestionDraft("side", "next question"));
		let retry!: Promise<void>;
		act(() => {
			retry = hook.result.current.send("side", "");
		});
		await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
		expect(post.mock.calls[1][1].body).toEqual(original);
		await act(async () => {
			accept({});
			await retry;
		});
		await waitFor(() =>
			expect(
				put.mock.calls.some(([, options]) => {
					const draft = JSON.parse(options.body.contentJson);
					return draft.text === "next question" && !draft.pendingDelivery;
				}),
			).toBe(true),
		);
	});

	it("saves the exact receipt before dispatch and restores it on remount", async () => {
		const hook = await beginSend("original");
		const frozen = post.mock.calls[0][1].body;
		const saved = put.mock.calls.find(([, options]) => JSON.parse(options.body.contentJson).pendingDelivery)?.[1].body
			.contentJson;
		expect(saved).toBeDefined();
		expect(put.mock.invocationCallOrder[0]).toBeLessThan(post.mock.invocationCallOrder[0]);
		await act(async () => {
			reject(new Error("lost"));
			await hook.sending;
		});
		hook.unmount();
		const oldGet = get.getMockImplementation()!;
		get.mockImplementation(async (path: string) =>
			path.endsWith("/draft") ? { data: { contentJson: saved } } : oldGet(path),
		);
		const recovered = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
		await waitFor(() => expect(recovered.result.current.activeId).toBe("side"));
		await waitFor(() => expect(get.mock.calls.filter(([path]) => path.endsWith("/draft")).length).toBeGreaterThan(1));
		let retry!: Promise<void>;
		act(() => {
			retry = recovered.result.current.send("side", "");
		});
		await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
		expect(post.mock.calls[1][1].body).toEqual(frozen);
		await act(async () => {
			accept({});
			await retry;
		});
	});

	it("retries image delivery without restaging its attachment", async () => {
		const stage = vi.fn(async () => ["/scratch/image.png"]);
		const hook = renderHook(() => useIndependentSideChats("session", [], [], stage, true));
		await waitFor(() => expect(hook.result.current.activeId).toBe("side"));
		act(() => hook.result.current.setQuestionDraft("side", "image question"));
		let sending!: Promise<void>;
		act(() => {
			sending = hook.result.current.send("side", "image question", [{ mimeType: "image/png", data: "cGl4ZWxz" }]);
		});
		await waitFor(() => expect(post).toHaveBeenCalledOnce());
		const original = post.mock.calls[0][1].body;
		expect(original.attachments).toEqual([{ mimeType: "image/png", data: "cGl4ZWxz" }]);
		await act(async () => {
			reject(new Error("response lost"));
			await sending;
		});
		let retry!: Promise<void>;
		act(() => {
			retry = hook.result.current.send("side", "");
		});
		await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
		expect(post.mock.calls[1][1].body).toEqual(original);
		expect(stage).toHaveBeenCalledOnce();
		await act(async () => {
			accept({});
			await retry;
		});
	});

	it("propagates a rejected main-to-side handoff so the main composer retains its draft", async () => {
		const hook = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
		await waitFor(() => expect(hook.result.current.activeId).toBe("side"));
		post.mockResolvedValue({ error: {}, response: { status: 409 } });
		await act(async () => {
			await expect(hook.result.current.send("side", "original main request", [], [], true)).rejects.toEqual({});
		});
	});

	it("does not recreate a closed draft after late acceptance", async () => {
		const hook = await beginSend("original");
		await act(async () => {
			await hook.result.current.close("side");
		});
		const savedCount = put.mock.calls.length;
		await act(async () => {
			accept({});
			await hook.sending;
		});
		expect(hook.result.current.sides).toEqual([]);
		expect(put.mock.calls.length).toBe(savedCount);
	});
});

const panelSides = ["side", "other"].map((id, index) => ({
	id,
	sessionId: "session",
	mainConversationId: "main",
	state: "ready",
	contextMode: "native",
	label: index === 0 ? "First question" : "Second question",
	createdAt: "2026-01-01T00:00:00Z",
	updatedAt: "2026-01-01T00:00:00Z",
}));

function SidePanelHarness() {
	const chat = useIndependentSideChats("session", [], [], async () => [], false);
	useEffect(() => {
		chat.show("side");
	}, []);
	return (
		<TooltipProvider>
			<button onClick={() => chat.show("side")}>Reopen side chat</button>
			<button onClick={() => chat.hide()}>Hide inspector</button>
			{chat.panel}
		</TooltipProvider>
	);
}

describe("side panel navigation and recovery", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		get.mockImplementation(async (path: string, options?: { params?: { path?: { sideId?: string } } }) => {
			if (path.endsWith("/side-chats")) return { data: { sides: panelSides } };
			const side = panelSides.find((item) => item.id === options?.params?.path?.sideId) ?? panelSides[0];
			if (path.endsWith("/draft"))
				return {
					data: {
						contentJson: JSON.stringify({
							version: 1,
							text: side.id === "side" ? "First draft" : "Second draft",
							attachments: [],
							references: [],
						}),
					},
				};
			return {
				data: {
					snapshot: {
						side,
						messages: [],
						turns: [],
						activities: [],
						hasMore: false,
					},
				},
			};
		});
		put.mockResolvedValue({});
		post.mockResolvedValue({ data: { side: panelSides[0] } });
		remove.mockResolvedValue({});
	});

	it.each([undefined, "", "   "])("renders a fallback for an absent or blank side label (%s)", async (label) => {
		const normalGet = get.getMockImplementation()!;
		get.mockImplementation(async (path, options) => {
			const result = await normalGet(path, options);
			if (path.endsWith("/side-chats"))
				return {
					data: {
						sides: panelSides.map((side) => ({
							...side,
							label: side.id === "side" ? label : side.label,
						})),
					},
				};
			if (result.data?.snapshot?.side.id === "side")
				result.data.snapshot.side = { ...result.data.snapshot.side, label };
			return result;
		});
		render(<SidePanelHarness />);
		expect(await screen.findByRole("tab", { name: "Side Chat" })).toHaveAttribute("aria-selected", "true");
		await screen.findByLabelText("Side chat question");
	});

	it("hides and reopens without closing the conversation or losing its draft", async () => {
		render(<SidePanelHarness />);
		const field = await screen.findByLabelText("Side chat question");
		await waitFor(() => expect(lexicalEditorText(field)).toBe("First draft"));
		await typeInLexicalEditor(field, " with an unsent follow-up");
		await userEvent.click(screen.getByRole("button", { name: "Hide inspector" }));
		expect(screen.queryByRole("complementary", { name: "Side chats" })).not.toBeInTheDocument();
		expect(remove).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Reopen side chat" }));
		await waitFor(() =>
			expect(lexicalEditorText(screen.getByLabelText("Side chat question"))).toBe(
				"First draft with an unsent follow-up",
			),
		);
	});

	it("shows the shorter isolation explanation without info or collapse controls", async () => {
        render(<SidePanelHarness />);
        await screen.findByLabelText("Side chat question");
        expect(screen.getByText(/Files and Git are shared/)).toBeVisible();
        expect(screen.queryByRole("button", { name: "About side-chat isolation" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Hide side chat" })).not.toBeInTheDocument();
    });

	it("offers a new side for an unverifiable legacy boundary without removing its transcript", async () => {
		const normalGet = get.getMockImplementation()!;
		get.mockImplementation(async (path, options) => {
			const result = await normalGet(path, options);
			if (result.data?.snapshot) result.data.snapshot.side = { ...result.data.snapshot.side, state: "failed", recreateRequired: true, errorMessage: "side-chat safety boundary cannot be verified; close this side and create a new one" };
			return result;
		});
		render(<SidePanelHarness />);
		await screen.findByText(/side-chat safety boundary cannot be verified/);
		expect(screen.queryByRole("button", { name: "Retry connection" })).not.toBeInTheDocument();
		await userEvent.click(screen.getAllByRole("button", { name: "New side chat" }).at(-1)!);
		await waitFor(() => expect(post).toHaveBeenCalled());
		expect(remove).not.toHaveBeenCalled();
	});

	it("switches tabs by keyboard without losing independent drafts", async () => {
		const user = userEvent.setup();
		render(<SidePanelHarness />);
		const first = await screen.findByRole("tab", { name: "First question" });
		first.focus();
		await user.keyboard("{ArrowRight}");
		expect(screen.getByRole("tab", { name: "Second question" })).toHaveAttribute("aria-selected", "true");
		await waitFor(() => expect(lexicalEditorText(screen.getByLabelText("Side chat question"))).toBe("Second draft"));
		await user.keyboard("{Home}");
		await waitFor(() => expect(lexicalEditorText(screen.getByLabelText("Side chat question"))).toBe("First draft"));
	});

	it("selects completed side text into its own annotated draft", async () => {
		const normalGet = get.getMockImplementation()!;
		get.mockImplementation(async (path, options) => {
			const result = await normalGet(path, options);
			if (result.data?.snapshot) {
				result.data.snapshot.turns = [{ id: "source-turn", state: "completed", createdAt: "2026-01-01T00:00:00Z" }];
				result.data.snapshot.messages = [
					{
						id: "self-source",
						turnId: "source-turn",
						sequence: 1,
						revision: 2,
						role: "assistant",
						text: "Juniper",
						streaming: false,
						createdAt: "2026-01-01T00:00:00Z",
					},
				];
			}
			return result;
		});
		render(<SidePanelHarness />);
		const text = await screen.findByText("Juniper");
		const log = text.closest('[role="log"]')!;
		expect(log).toHaveClass("select-text");
		const range = document.createRange();
		range.selectNodeContents(text);
		const selection = window.getSelection()!;
		selection.removeAllRanges();
		selection.addRange(range);
		vi.spyOn(Range.prototype, "getClientRects").mockReturnValue([
			new DOMRect(40, 150, 100, 20),
		] as unknown as DOMRectList);
		vi.spyOn(log, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 600, 500));
		fireEvent.mouseUp(log);
		fireEvent.click(screen.getByRole("button", { name: "Add to chat" }));
		await waitFor(() =>
			expect(
				put.mock.calls.some(([, options]) => {
					const draft = JSON.parse(options.body.contentJson);
					return (
						draft.references?.[0]?.conversationId === "side" &&
						draft.references[0].messageId === "self-source" &&
						draft.references[0].revision === 2
					);
				}),
			).toBe(true),
		);
		selection.removeAllRanges();
		const scrollTo = vi.fn();
		Object.defineProperty(log, "scrollTo", { configurable: true, value: scrollTo });
		const rangeBounds = vi
			.spyOn(Range.prototype, "getBoundingClientRect")
			.mockReturnValue(new DOMRect(40, 150, 100, 20));
		await userEvent.click(screen.getByRole("button", { name: "1 annotation" }));
		await userEvent.click(await screen.findByRole("button", { name: "Juniper" }));
		await waitFor(() => expect(text.closest("[data-chat-message-body]")).toHaveClass("chat-annotation-target"));
		expect(scrollTo).not.toHaveBeenCalled();
		rangeBounds.mockReturnValue(new DOMRect(40, 800, 100, 20));
		await userEvent.click(screen.getByRole("button", { name: "1 annotation" }));
		await userEvent.click(await screen.findByRole("button", { name: "Juniper" }));
		await waitFor(() => expect(scrollTo).toHaveBeenCalledWith({ top: 560, behavior: "smooth" }));
		vi.restoreAllMocks();
	});

	it("shows no conversation tab and creates nothing until + is clicked", async () => {
		get.mockResolvedValue({ data: { sides: [] } });
		render(<SidePanelHarness />);
		await screen.findByText("No side chat opened yet");
		expect(screen.queryAllByRole("tab")).toHaveLength(0);
		expect(post).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "New side chat" }));
		await waitFor(() =>
			expect(post).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/conversation/side-chats",
				expect.objectContaining({ body: expect.objectContaining({ forceNew: true }) }),
			),
		);
	});
	it("renames with F2 and Enter and cancels with Escape", async () => {
		patch.mockResolvedValue({});
		render(<SidePanelHarness />);
		const tab = await screen.findByRole("tab", { name: "First question" });
		fireEvent.keyDown(tab, { key: "F2" });
		const input = screen.getByRole("textbox", { name: "Rename side chat" });
		await userEvent.clear(input);
		await userEvent.type(input, "Side Chat 99");
		fireEvent.keyDown(input, { key: "Enter" });
		await waitFor(() =>
			expect(patch).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/label",
				expect.objectContaining({
					params: { path: { sessionId: "session", sideId: "side" } },
					body: { label: "Side Chat 99" },
				}),
			),
		);
		await waitFor(() => expect(screen.queryByRole("textbox", { name: "Rename side chat" })).not.toBeInTheDocument());
		fireEvent.doubleClick(screen.getByRole("tab", { name: "Second question" }));
		fireEvent.keyDown(screen.getByRole("textbox", { name: "Rename side chat" }), { key: "Escape" });
		expect(patch).toHaveBeenCalledOnce();
	});
	it("offers only Rename on a tab's right-click menu", async () => {
		render(<SidePanelHarness />);
		const tab = await screen.findByRole("tab", { name: "Second question" });
		fireEvent.contextMenu(tab, { button: 2 });
		expect(await screen.findByRole("menuitem", { name: "Rename" })).toBeVisible();
		expect(screen.queryByRole("menuitem", { name: "Close" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Rename" }));
		expect(screen.getByRole("textbox", { name: "Rename side chat" })).toHaveValue("Second question");
	});
	it("selects the existing name and commits on blur, discarding empty or unchanged names", async () => {
		patch.mockResolvedValue({});
		render(<SidePanelHarness />);
		const tab = await screen.findByRole("tab", { name: "First question" });
		fireEvent.doubleClick(tab);
		const input = screen.getByRole("textbox", { name: "Rename side chat" }) as HTMLInputElement;
		expect(input.selectionStart).toBe(0);
		expect(input.selectionEnd).toBe("First question".length);
		await userEvent.clear(input);
		await userEvent.type(input, "  Notes  ");
		fireEvent.blur(input);
		await waitFor(() => expect(patch).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/label",
			expect.objectContaining({ body: { label: "Notes" } }),
		));
		for (const value of ["", "First question"]) {
			fireEvent.doubleClick(screen.getByRole("tab", { name: "First question" }));
			const edit = screen.getByRole("textbox", { name: "Rename side chat" });
			fireEvent.change(edit, { target: { value } });
			fireEvent.blur(edit);
			expect(screen.queryByRole("textbox", { name: "Rename side chat" })).not.toBeInTheDocument();
		}
		expect(patch).toHaveBeenCalledOnce();
	});
	it("confirms closing an inactive tab without changing the active draft", async () => {
		render(<SidePanelHarness />);
		await screen.findByLabelText("Side chat question");
		await userEvent.click(screen.getByRole("button", { name: "Close Second question" }));
		expect(await screen.findByRole("dialog", { name: "Close Second question?" })).toBeInTheDocument();
		expect(remove).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Keep side chat" }));
		expect(remove).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Close Second question" }));
		await userEvent.click(screen.getByRole("button", { name: "Close side chat" }));
		await waitFor(() => expect(screen.queryByRole("tab", { name: "Second question" })).not.toBeInTheDocument());
		expect(screen.getByRole("tab", { name: "First question" })).toHaveAttribute("aria-selected", "true");
		expect(lexicalEditorText(screen.getByLabelText("Side chat question"))).toBe("First draft");
	});
	it("keeps the tab and confirmation open when closing fails", async () => {
		remove.mockResolvedValue({ error: {} });
		render(<SidePanelHarness />);
		await screen.findByLabelText("Side chat question");
		await userEvent.click(screen.getByRole("button", { name: "Close First question" }));
		await userEvent.click(screen.getByRole("button", { name: "Close side chat" }));
		await waitFor(() => expect(screen.getByRole("dialog")).toHaveTextContent("Send failed"));
		await userEvent.click(screen.getByRole("button", { name: "Keep side chat" }));
		expect(screen.getByRole("tab", { name: "First question" })).toBeInTheDocument();
	});

	it("retries a failed initial snapshot GET without sending or recovering the provider", async () => {
		const normalGet = get.getMockImplementation()!;
		let unavailable = true;
		get.mockImplementation(async (path, options) =>
			path.endsWith("/{sideId}") && unavailable ? { error: {} } : normalGet(path, options),
		);
		render(<SidePanelHarness />);
		const retry = await screen.findByRole("button", { name: "Retry loading" });
		expect(screen.queryByLabelText("Side chat question")).not.toBeInTheDocument();
		get.mockClear();
		unavailable = false;
		await userEvent.click(retry);
		await screen.findByLabelText("Side chat question");
		expect(get).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", {
			params: { path: { sessionId: "session", sideId: "side" } },
		});
		expect(post).not.toHaveBeenCalled();
		expect(remove).not.toHaveBeenCalled();
	});

	it("does not offer loading recovery for a draft save error on a loaded thread", async () => {
		put.mockResolvedValue({ error: {} });
		render(<SidePanelHarness />);
		await screen.findByLabelText("Side chat question");
		await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Send failed"));
		expect(screen.queryByRole("button", { name: "Retry loading" })).not.toBeInTheDocument();
		expect(screen.getByLabelText("Side chat question")).toBeInTheDocument();
	});

	it("only shows uncertain delivery after a pending send fails", async () => {
		let fail!: (error: Error) => void;
		post.mockImplementation(
			() =>
				new Promise((_, reject) => {
					fail = reject;
				}),
		);
		render(<SidePanelHarness />);
		const field = await screen.findByLabelText("Side chat question");
		await waitFor(() => expect(lexicalEditorText(field)).toBe("First draft"));
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(post).toHaveBeenCalledOnce());
		expect(screen.queryByText(/Delivery is unconfirmed/)).not.toBeInTheDocument();
		await act(async () => fail(new Error("Connection lost")));
		expect(await screen.findByText(/Delivery is unconfirmed/)).toBeInTheDocument();
	});

	it("explicit New requests a separate side chat", async () => {
		render(<SidePanelHarness />);
		await screen.findByLabelText("Side chat question");
		await userEvent.click(screen.getByRole("button", { name: "New side chat" }));
		await waitFor(() =>
			expect(post).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/conversation/side-chats",
				expect.objectContaining({
					params: { path: { sessionId: "session" } },
					body: expect.objectContaining({ forceNew: true }),
				}),
			),
		);
		expect(screen.getAllByRole("tab")).toHaveLength(2);
		expect(remove).not.toHaveBeenCalled();
	});
});

it("does not call the local daemon for a remote session", async () => {
	vi.clearAllMocks();
	const hook = renderHook(() => useIndependentSideChats("remote-session", [], [], async () => [], false, false));
	await act(async () => {
		await Promise.resolve();
	});
	expect(get).not.toHaveBeenCalled();
	await expect(hook.result.current.create()).rejects.toThrow("only for local AO sessions");
	expect(post).not.toHaveBeenCalled();
});
