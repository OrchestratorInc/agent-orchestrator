import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	ids: 0,
	theme: new Proxy({}, { get: () => "#000000" }),
}));
vi.mock("react-native", () => ({
	Platform: { OS: "android" }, View: "View", Text: "Text", TextInput: "TextInput", Pressable: "Pressable", ActivityIndicator: "ActivityIndicator",
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
}));
vi.mock("../haptics", () => ({ haptics: { success: vi.fn(), error: vi.fn(), tap: vi.fn() } }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme) }));
vi.mock("../textSelection", () => ({ textSelectionColors: () => ({}) }));
vi.mock("./useConversation", () => ({ clientMessageId: () => `id-${++fixtures.ids}` }));

import { InlineMessageEditor, messageEditDraft, type MessageEditDraft } from "./InlineMessageEditor";
import { withAttachmentReferences } from "./messageAttachments";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.ids = 0;
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

function draft(attachments: string[] = []): MessageEditDraft {
	return messageEditDraft({ turnId: "t1", initialText: "Fix the bug", attachments });
}
function open(value: MessageEditDraft, onEdit: (turnId: string, text: string, id: string) => Promise<void>, { onCancel = vi.fn(), blocked = false } = {}) {
	act(() => { renderer = create(<InlineMessageEditor draft={value} blocked={blocked} onCancel={onCancel} onEdit={onEdit} />); });
	return onCancel;
}
const input = () => renderer!.root.findByType("TextInput" as never);
const button = (label: string) => renderer!.root.findByProps({ accessibilityLabel: label });
async function send() { await act(async () => { button("Send edited message").props.onPress(); }); }

it("waits for a changed body, then sends it with the original attachments", async () => {
	const value = draft([".ao/attachments/image-a.png"]);
	const onEdit = vi.fn(async () => {});
	open(value, onEdit);
	expect(button("Send edited message").props.disabled).toBe(true);
	act(() => input().props.onChangeText("Fix the other bug"));
	// The timeline's record keeps the draft if the row scrolls away and back.
	expect(value.text).toBe("Fix the other bug");
	expect(button("Send edited message").props.disabled).toBe(false);
	await send();
	expect(onEdit).toHaveBeenCalledExactlyOnceWith("t1", withAttachmentReferences("Fix the other bug", [".ao/attachments/image-a.png"]), "id-1");
});

it("retries an uncertain edit under the same delivery id but mints a new one after a refusal", async () => {
	const onEdit = vi.fn<(turnId: string, text: string, id: string) => Promise<void>>()
		.mockRejectedValueOnce(Object.assign(new Error("Your machine may already have the edit."), { code: "CHAT_EDIT_UNCERTAIN" }))
		.mockRejectedValueOnce(Object.assign(new Error("Stop the current turn."), { code: "CHAT_EDIT_BUSY" }))
		.mockResolvedValueOnce(undefined);
	open(draft(), onEdit);
	act(() => input().props.onChangeText("Edited"));
	await send();
	expect(renderer!.root.findByProps({ accessibilityRole: "alert" }).props.children).toBe("Your machine may already have the edit.");
	await send();
	await send();
	expect(onEdit.mock.calls.map(([, , id]) => id)).toEqual(["id-1", "id-1", "id-2"]);
});

it("holds Send while the agent is working and leaves on Cancel", () => {
	const value = draft();
	value.text = "Changed";
	const onCancel = open(value, vi.fn(async () => {}), { blocked: true });
	expect(button("Send edited message").props.disabled).toBe(true);
	// No hint text above the field: the editor is just the field and its controls.
	expect(renderer!.root.findAllByType("Text" as never)).toHaveLength(0);
	act(() => button("Cancel edit").props.onPress());
	expect(onCancel).toHaveBeenCalledTimes(1);
});
