import { readFileSync } from "node:fs";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

vi.mock("./InlineMessageEditor", () => ({
	messageEditDraft: (input: { turnId: string; initialText: string; attachments: string[] }) => ({ ...input, text: input.initialText, deliveryId: "id-1" }),
}));

import { useMessageEdit } from "./useMessageEdit";
import type { ConversationItem, ConversationMessage } from "./types";

type Edit = ReturnType<typeof useMessageEdit>;

const prompt = (turnId: string, sequence: number): ConversationMessage => ({ kind: "message", id: `u-${turnId}`, turnId, sequence, revision: 1, role: "user", origin: "human", text: `Prompt ${turnId}`, streaming: false, createdAt: "" });
const reply = (turnId: string, sequence: number): ConversationItem => ({ kind: "message", id: `a-${turnId}`, turnId, sequence, revision: 1, role: "assistant", origin: "provider", text: "Done", streaming: false, createdAt: "" });

let renderer: ReactTestRenderer | undefined;
let latest: Edit | undefined;
const onEditingChange = vi.fn();

function Harness({ items }: { items: ConversationItem[] }) {
	latest = useMessageEdit({ items, onEditingChange });
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
