import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";

vi.mock("react-native", () => ({ BackHandler: { addEventListener: () => ({ remove: () => {} }) }, Keyboard: { addListener: () => ({ remove: () => {} }) } }));

import { useChatEffort } from "./useChatEffort";

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
const models = [
	{ id: "opus", displayName: "Opus", efforts: ["low", "medium", "high"], defaultEffort: "low" },
	{ id: "haiku", displayName: "Haiku" },
] as ChatModel[];

let renderer: ReactTestRenderer | undefined;
let effort: ReturnType<typeof useChatEffort>;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

function render(over: { snapshot?: ConversationSnapshot; disabled?: boolean; models?: ChatModel[] } = {}) {
	const onSettings = vi.fn(() => Promise.resolve());
	const onOption = vi.fn(() => Promise.resolve([] as ChatConfigOption[]));
	function Harness() {
		effort = useChatEffort({ snapshot: over.snapshot ?? snapshot(), models: over.models ?? models, options: [], disabled: over.disabled, onSettings, onOption });
		return null;
	}
	act(() => { renderer = create(<Harness />); });
	return { onSettings, onOption };
}

describe("chat effort", () => {
	it("offers the model's levels with the one in effect chosen", () => {
		render();
		expect(effort.choices).toEqual([
			{ value: "low", label: "Low", selected: true },
			{ value: "medium", label: "Medium", selected: false },
			{ value: "high", label: "High", selected: false },
		]);
		render({ snapshot: snapshot({ settings: { model: "haiku", approvalMode: "auto" } }) });
		expect(effort.choices).toEqual([]);
	});

	// A drag walks the levels; the conversation hears only where it stopped.
	it("saves once, the level the finger lifted on, and stays open", () => {
		const { onSettings } = render();
		act(() => effort.toggle());
		expect(effort.open).toBe(true);
		act(() => { effort.choose("medium"); effort.choose("high"); });
		expect(onSettings).not.toHaveBeenCalled();
		act(() => effort.release());
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "opus", approvalMode: "auto", reasoningEffort: "high" });
		expect(effort.open).toBe(true);
		act(() => effort.close());
		expect(onSettings).toHaveBeenCalledOnce();
	});

	it("saves a pick the finger never lifted on when the slider closes", () => {
		const { onSettings } = render();
		act(() => effort.toggle());
		act(() => effort.choose("medium"));
		act(() => effort.toggle());
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "opus", approvalMode: "auto", reasoningEffort: "medium" });
		expect(effort.open).toBe(false);
	});

	it("saves nothing when let go on the level already in effect", () => {
		const { onSettings } = render();
		act(() => effort.toggle());
		act(() => { effort.choose("high"); effort.choose("low"); });
		act(() => effort.release());
		expect(onSettings).not.toHaveBeenCalled();
	});

	// A model that names no default and a conversation that reports no level.
	it("shows Medium when nothing names a level, saving it only once picked", () => {
		const undecided = [{ id: "opus", displayName: "Opus", efforts: ["low", "medium", "high", "max"] }] as ChatModel[];
		const { onSettings } = render({ models: undecided });
		expect(effort.choices.find((choice) => choice.selected)?.value).toBe("medium");
		expect(onSettings).not.toHaveBeenCalled();
		act(() => effort.toggle());
		act(() => { effort.choose("medium"); effort.release(); });
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "opus", approvalMode: "auto", reasoningEffort: "medium" });
	});

	it("never opens while the conversation cannot take a change", () => {
		render({ disabled: true });
		act(() => effort.toggle());
		expect(effort.open).toBe(false);
	});
});
