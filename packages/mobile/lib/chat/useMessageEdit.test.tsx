import { readFileSync } from "node:fs";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const ids = vi.hoisted(() => ({ next: 0 }));
vi.mock("./useConversation", () => ({ clientMessageId: () => `id-${++ids.next}` }));
vi.mock("./InlineMessageEditor", () => ({
	messageEditDraft: (input: { turnId: string; initialText: string; attachments: string[] }) => ({ ...input, text: input.initialText, deliveryId: `id-${++ids.next}` }),
}));

import { useMessageEdit } from "./useMessageEdit";
import type { ConversationItem, ConversationMessage } from "./types";

type Edit = ReturnType<typeof useMessageEdit>;

const prompt = (turnId: string, sequence: number): ConversationMessage => ({ kind: "message", id: `u-${turnId}`, turnId, sequence, revision: 1, role: "user", origin: "human", text: `Prompt ${turnId}`, streaming: false, createdAt: "" });
const reply = (turnId: string, sequence: number): ConversationItem => ({ kind: "message", id: `a-${turnId}`, turnId, sequence, revision: 1, role: "assistant", origin: "provider", text: "Done", streaming: false, createdAt: "" });

let renderer: ReactTestRenderer | undefined;
let latest: Edit | undefined;
const onEditingChange = vi.fn();
const onEditMessage = vi.fn<(turnId: string, text: string, clientMessageId: string) => Promise<void>>();

function Harness({ items }: { items: ConversationItem[] }) {
	latest = useMessageEdit({ items, onEditingChange, onEditMessage });
	return null;
}
function render(items: ConversationItem[]) {
	act(() => {
		if (renderer) renderer.update(<Harness items={items} />);
		else renderer = create(<Harness items={items} />);
	});
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	onEditingChange.mockClear();
	onEditMessage.mockReset();
	ids.next = 0;
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	latest = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

it("keeps an edit open across refreshes that still hold its prompt", () => {
	const items = [prompt("t1", 1), reply("t1", 2), prompt("t2", 3)];
	render(items);
	act(() => latest!.startEdit(items[0] as ConversationMessage));
	expect(latest!.editing).toBe(true);
	expect(onEditingChange).toHaveBeenLastCalledWith(true);
	render([...items, reply("t2", 4)]);
	expect(latest!.editDraft?.turnId).toBe("t1");
	expect(onEditingChange).toHaveBeenLastCalledWith(true);
});

it("closes the edit and gives the composer back when its prompt leaves the conversation", () => {
	const items = [prompt("t1", 1), reply("t1", 2), prompt("t2", 3)];
	render(items);
	act(() => latest!.startEdit(items[0] as ConversationMessage));
	expect(onEditingChange).toHaveBeenLastCalledWith(true);
	// Another device edited or rolled back that message, so its turn is gone.
	render([prompt("t2", 3)]);
	expect(latest!.editing).toBe(false);
	expect(latest!.editDraft).toBeUndefined();
	expect(onEditingChange).toHaveBeenLastCalledWith(false);
	// The turn coming back later does not reopen a discarded edit.
	render(items);
	expect(latest!.editing).toBe(false);
});

it("hides the composer only while the timeline reports an open edit", () => {
	const screen = readFileSync(new URL("./ChatSessionScreen.tsx", import.meta.url), "utf8");
	expect(screen).toContain("onEditingChange={setEditingMessage}");
	expect(screen).toContain("<View style={editingMessage ? styles.composerHidden : undefined}>");
});

const uncertain = () => Object.assign(new Error("Your machine may already have the edit."), { code: "CHAT_EDIT_UNCERTAIN" });
async function submit(payload: string) {
	let failure: unknown;
	await act(async () => { await latest!.submitEdit(payload, payload).catch((cause: unknown) => { failure = cause; }); });
	return failure;
}

it("keeps an unresolved delivery open with its exact payload and id after its prompt leaves", async () => {
	const items = [prompt("t1", 1), reply("t1", 2)];
	render(items);
	act(() => latest!.startEdit(items[0] as ConversationMessage));
	onEditMessage.mockRejectedValueOnce(uncertain());
	expect(await submit("banana")).toBeTruthy();
	expect(latest!.editDraft?.pending).toEqual({ body: "banana", payload: "banana" });
	// The daemon may already have branched, which removes the source prompt.
	render([prompt("t9", 3)]);
	expect(latest!.editing).toBe(true);
	expect(latest!.detached).toBe(true);
	expect(onEditingChange).toHaveBeenLastCalledWith(true);
	onEditMessage.mockResolvedValueOnce(undefined);
	await submit("banana");
	expect(onEditMessage.mock.calls.map(([turnId, text, id]) => [turnId, text, id])).toEqual([["t1", "banana", "id-1"], ["t1", "banana", "id-1"]]);
	expect(latest!.editing).toBe(false);
	expect(onEditingChange).toHaveBeenLastCalledWith(false);
});

it("mints a fresh delivery id after a definitive refusal and closes normally when the prompt leaves", async () => {
	const items = [prompt("t1", 1), reply("t1", 2)];
	render(items);
	act(() => latest!.startEdit(items[0] as ConversationMessage));
	onEditMessage.mockRejectedValueOnce(Object.assign(new Error("Stop the current turn."), { code: "CHAT_EDIT_BUSY" }));
	await submit("banana");
	expect(latest!.editDraft?.pending).toBeUndefined();
	expect(latest!.editDraft?.deliveryId).toBe("id-2");
	render([prompt("t9", 3)]);
	expect(latest!.editing).toBe(false);
});

it("abandons an unresolved delivery only when asked", async () => {
	const items = [prompt("t1", 1)];
	render(items);
	act(() => latest!.startEdit(items[0] as ConversationMessage));
	onEditMessage.mockRejectedValueOnce(new Error("network"));
	await submit("banana");
	expect(latest!.editDraft?.pending).toBeDefined();
	act(() => latest!.cancelEdit());
	expect(latest!.editing).toBe(false);
	expect(onEditingChange).toHaveBeenLastCalledWith(false);
});


it("opens an edit on what the user typed, without the worker reports folded into it", () => {
	const folded: ConversationMessage = { ...prompt("t1", 1), text: "Which workers can stop?\n\n<ao-worker-reports>\nReports since your previous turn:\n\n[done] ao://sessions/p/w-1\nDone.\n</ao-worker-reports>" };
	render([folded, reply("t1", 2)]);
	act(() => latest!.startEdit(folded));
	expect(latest!.editDraft).toMatchObject({ turnId: "t1", initialText: "Which workers can stop?" });
});
