import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";

vi.mock("react-native", () => ({ BackHandler: { addEventListener: () => ({ remove: () => {} }) }, Keyboard: { addListener: () => ({ remove: () => {} }) } }));

import { useEffortSliderOpen } from "../useEffortSlider";
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
let slider: ReturnType<typeof useEffortSliderOpen>;
let composerRenders = 0;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

function render(over: { snapshot?: ConversationSnapshot; disabled?: boolean; models?: ChatModel[] } = {}) {
	const onSettings = vi.fn(() => Promise.resolve());
	const onOption = vi.fn(() => Promise.resolve([] as ChatConfigOption[]));
	// The slider's side as EffortSliderHost has it: subscribed to the
	// composer's switch on its own.
	function SliderHost({ chat }: { chat: ReturnType<typeof useChatEffort> }) {
		slider = useEffortSliderOpen(chat.choices, chat.busy, chat.commit, chat.slider);
		return null;
	}
	function Harness() {
		composerRenders += 1;
		effort = useChatEffort({ snapshot: over.snapshot ?? snapshot(), models: over.models ?? models, options: [], disabled: over.disabled, onSettings, onOption });
		return <SliderHost chat={effort} />;
	}
	composerRenders = 0;
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

	it("gives the agent's own effort no stop, and shows the fallback level while it is in effect", () => {
		const native = [{ id: "opus", displayName: "Opus", default: true, efforts: ["default", "low", "medium", "high"] }] as ChatModel[];
		render({ models: native, snapshot: snapshot({ settings: { model: "opus", approvalMode: "auto", reasoningEffort: "default" } }) });
		expect(effort.choices).toEqual([
			{ value: "low", label: "Low", selected: false },
			{ value: "medium", label: "Medium", selected: true },
			{ value: "high", label: "High", selected: false },
		]);
	});

	// A drag walks the levels; the conversation hears only where it stopped.
	it("saves once, the level the finger lifted on, and stays open", () => {
		const { onSettings } = render();
		act(() => slider.toggle());
		expect(slider.open).toBe(true);
		act(() => { effort.choose("medium"); effort.choose("high"); });
		expect(onSettings).not.toHaveBeenCalled();
		act(() => effort.commit());
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "opus", approvalMode: "auto", reasoningEffort: "high" });
		expect(slider.open).toBe(true);
		act(() => effort.close());
		expect(onSettings).toHaveBeenCalledOnce();
	});

	it("saves a pick the finger never lifted on when the slider closes", () => {
		const { onSettings } = render();
		act(() => slider.toggle());
		act(() => effort.choose("medium"));
		act(() => slider.toggle());
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "opus", approvalMode: "auto", reasoningEffort: "medium" });
		expect(slider.open).toBe(false);
	});

	it("saves nothing when let go on the level already in effect", () => {
		const { onSettings } = render();
		act(() => slider.toggle());
		act(() => { effort.choose("high"); effort.choose("low"); });
		act(() => effort.commit());
		expect(onSettings).not.toHaveBeenCalled();
	});

	// A model that names no default and a conversation that reports no level.
	// Back on the model's own level, the conversation follows its default again
	// rather than being pinned to that level for good.
	it("clears the pick when the slider goes back to the model's default level", () => {
		const { onSettings } = render({ snapshot: snapshot({ settings: { model: "opus", approvalMode: "auto", reasoningEffort: "high" } }) });
		act(() => slider.toggle());
		act(() => { effort.choose("low"); effort.commit(); });
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "opus", approvalMode: "auto", reasoningEffort: undefined });
	});

	it("shows Medium when nothing names a level, saving it only once picked", () => {
		const undecided = [{ id: "opus", displayName: "Opus", efforts: ["low", "medium", "high", "max"] }] as ChatModel[];
		const { onSettings } = render({ models: undecided });
		expect(effort.choices.find((choice) => choice.selected)?.value).toBe("medium");
		expect(onSettings).not.toHaveBeenCalled();
		act(() => slider.toggle());
		act(() => { effort.choose("medium"); effort.commit(); });
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "opus", approvalMode: "auto", reasoningEffort: "medium" });
	});

	// Re-rendering a whole chat composer on each open held the slider back by
	// half a second on a phone.
	it("opens and closes without re-rendering the composer", () => {
		render();
		const before = composerRenders;
		act(() => slider.toggle());
		expect(slider.open).toBe(true);
		act(() => effort.close());
		expect(slider.open).toBe(false);
		expect(composerRenders).toBe(before);
	});

	it("never opens while the conversation cannot take a change", () => {
		render({ disabled: true });
		act(() => slider.toggle());
		expect(slider.open).toBe(false);
	});
});
