import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatConfigOption, ConversationSnapshot } from "./types";

vi.mock("react-native", () => ({ BackHandler: { addEventListener: () => ({ remove: () => {} }) }, Keyboard: { addListener: () => ({ remove: () => {} }) } }));

import { useChatPermissions } from "./useChatPermissions";

const snapshot = (over: Partial<ConversationSnapshot> = {}): ConversationSnapshot => ({
	conversationId: "c",
	sessionId: "s",
	harness: "claude-code",
	mode: "chat",
	controller: { state: "ready" },
	latestSequence: 0,
	oldestSequence: 0,
	hasMoreBefore: false,
	turns: [],
	items: [],
	settings: { model: "opus", approvalMode: "auto" },
	capabilities: [],
	...over,
});

let renderer: ReactTestRenderer | undefined;
let permissions: ReturnType<typeof useChatPermissions>;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

function render(over: { snapshot?: ConversationSnapshot; disabled?: boolean } = {}) {
	const onSettings = vi.fn(() => Promise.resolve());
	const onOption = vi.fn(() => Promise.resolve([] as ChatConfigOption[]));
	function Harness() {
		permissions = useChatPermissions({ snapshot: over.snapshot ?? snapshot(), models: [], options: [], disabled: over.disabled, onSettings, onOption });
		return null;
	}
	act(() => { renderer = create(<Harness />); });
	return { onSettings, onOption };
}

describe("chat permissions", () => {
	it("offers the modes in desktop's words, each with the look of what it does", () => {
		render();
		expect(permissions.choices.map((choice) => [choice.value, choice.label, choice.selected, choice.look])).toEqual([
			["accept-edits", "Accept edits", false, "accept-edits"],
			["auto", "Auto-approve", true, "auto"],
			["bypass-permissions", "Bypass permissions", false, "bypass-permissions"],
			["default", "Use agent permissions", false, "default"],
		]);
	});

	// Codex's default is full access, so it looks like bypassing.
	it("gives Codex's full access the look of bypassing", () => {
		render({ snapshot: snapshot({ harness: "codex", settings: { model: "opus" } }) });
		expect(permissions.choices[0]).toMatchObject({ label: "Full access", look: "bypass-permissions", selected: true });
	});

	// Antigravity is not offered bypass-permissions, yet an older chat can be
	// saved with it; the button must still show it, in the warning look.
	it("keeps an Antigravity chat's saved bypass mode selected, with its warning look", () => {
		render({ snapshot: snapshot({ harness: "agy", settings: { model: "opus", approvalMode: "bypass-permissions" } }) });
		expect(permissions.choices.filter((choice) => choice.selected)).toEqual([
			expect.objectContaining({ value: "bypass-permissions", label: "Bypass permissions", look: "bypass-permissions" }),
		]);
		// Not a second "Auto-approve" beside `auto`.
		expect(permissions.choices.filter((choice) => choice.label === "Auto-approve").map((choice) => choice.value)).toEqual(["auto"]);
	});

	it("saves a new mode and leaves the one in force alone", () => {
		const { onSettings } = render();
		act(() => permissions.choose("auto"));
		expect(onSettings).not.toHaveBeenCalled();
		act(() => permissions.choose("bypass-permissions"));
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "opus", approvalMode: "bypass-permissions" });
	});

	it("is busy while the conversation cannot take a change", () => {
		render({ disabled: true });
		expect(permissions.busy).toBe(true);
	});
});
