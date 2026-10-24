import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";
import type { ConversationSnapshot } from "./types";
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
vi.mock("react-native", () => ({ ActivityIndicator: "ActivityIndicator", Pressable: "Pressable", ScrollView: "ScrollView", StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 }, Switch: "Switch", Text: "Text", View: "View" }));
vi.mock("@expo/ui", () => ({ Host: "Host", Slider: "Slider", Switch: "Switch" }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}), useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("../ui", () => ({ SheetHeader: "SheetHeader" }));
import { ChatSettingsSheet } from "./ChatSettingsModal";
import { ChatSettingsSheet as AndroidChatSettingsSheet } from "./ChatSettingsModal.android";

const snapshot: ConversationSnapshot = { conversationId: "c1", sessionId: "s1", harness: "codex", mode: "chat", controller: { state: "ready" }, turns: [], items: [], settings: {}, latestSequence: 0, oldestSequence: 0, hasMoreBefore: false };
const callbacks = { onRefresh: () => {}, onSettings: async () => {}, onOption: async () => {} };
const visibleText = (rendered: ReactTestRenderer) => rendered.root.findAllByType("Text" as never).flatMap((node) => node.children).filter((child) => typeof child === "string").join(" ");

describe.each([["base", ChatSettingsSheet], ["Android", AndroidChatSettingsSheet]] as const)("%s Cloud approval controls", (_name, Sheet) => {
	it("does not offer approval overrides for a read-only Cloud session", () => {
		let rendered!: ReactTestRenderer;
		act(() => { rendered = create(<Sheet snapshot={{ ...snapshot, approvalModes: [] } as ConversationSnapshot} models={[]} options={[]} {...callbacks} />); });
		try {
			const visible = visibleText(rendered);
			expect(visible).not.toContain("Approvals");
			expect(visible).not.toContain("Permission mode");
			expect(visible).not.toContain("Never ask");
		} finally { act(() => rendered.unmount()); }
	});
	it.each([false, true])("restricts Cloud choices and preserves local choices (local=%s)", (local) => {
		let rendered!: ReactTestRenderer;
		const current = { ...snapshot, ...(local ? {} : { approvalModes: ["accept-edits", "auto"] }) } as ConversationSnapshot;
		act(() => { rendered = create(<Sheet snapshot={current} models={[]} options={[]} {...callbacks} />); });
		try {
			const permission = rendered.root.findAllByType("Pressable" as never).find((button) => button.findAllByType("Text" as never).some((text) => text.props.children === "Permission mode"));
			if (permission) act(() => permission.props.onPress());
			const visible = visibleText(rendered);
			expect(visible).toContain("Ask outside worktree");
			expect(visible).toContain("Ask when unsure");
			if (local) { expect(visible).toContain("Never ask"); expect(visible).toContain("Full access"); }
			else { expect(visible).not.toContain("Never ask"); expect(visible).not.toContain("Full access"); }
		} finally { act(() => rendered.unmount()); }
	});
});
