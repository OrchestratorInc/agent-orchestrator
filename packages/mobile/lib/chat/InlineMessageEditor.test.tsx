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
function open(value: MessageEditDraft, onEdit: (payload: string, body: string) => Promise<void>, { onCancel = vi.fn(), blocked = false } = {}) {
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
	expect(onEdit).toHaveBeenCalledExactlyOnceWith(withAttachmentReferences("Fix the other bug", [".ao/attachments/image-a.png"]), "Fix the other bug");
});

it("locks an unresolved delivery to its exact payload and offers Retry and Abandon, even while blocked", async () => {
	const value = draft([".ao/attachments/image-a.png"]);
	const payload = withAttachmentReferences("Sent once", [".ao/attachments/image-a.png"]);
	value.pending = { body: "Sent once", payload };
	const onEdit = vi.fn(async () => {});
	const onCancel = open(value, onEdit, { blocked: true });
	expect(input().props.value).toBe("Sent once");
	expect(input().props.editable).toBe(false);
	await act(async () => { button("Retry edit safely").props.onPress(); });
	expect(onEdit).toHaveBeenCalledExactlyOnceWith(payload, "Sent once");
	act(() => button("Abandon edit recovery").props.onPress());
	expect(onCancel).toHaveBeenCalledTimes(1);
});

it("explains a blocked Send on tap instead of sending, and leaves on Cancel", async () => {
	const value = draft();
	value.text = "Changed";
	const onEdit = vi.fn(async () => {});
	const onCancel = open(value, onEdit, { blocked: true });
	// No hint text above the field: the editor is just the field and its controls.
	expect(renderer!.root.findAllByType("Text" as never)).toHaveLength(0);
	// Send reads as unavailable but still answers a tap with the reason.
	expect(button("Send edited message").props.accessibilityState.disabled).toBe(true);
	expect(button("Send edited message").props.disabled).toBe(false);
	await send();
	expect(onEdit).not.toHaveBeenCalled();
	const alert = () => renderer!.root.findAll((node) => node.props.accessibilityRole === "alert" && (node.type as unknown) === "Text");
	expect(alert()[0]?.props.children).toBe("Stop the current turn to edit.");
	// The reason clears once the agent is idle again.
	act(() => renderer!.update(<InlineMessageEditor draft={value} blocked={false} onCancel={onCancel} onEdit={onEdit} />));
	expect(alert()).toHaveLength(0);
	act(() => button("Cancel edit").props.onPress());
	expect(onCancel).toHaveBeenCalledTimes(1);
});
