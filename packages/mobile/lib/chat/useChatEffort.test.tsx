import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";

const stored = vi.hoisted(() => new Map<string, string>());
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: {
		getItem: async (key: string) => stored.get(key) ?? null,
		setItem: async (key: string, value: string) => { stored.set(key, value); },
		removeItem: async (key: string) => { stored.delete(key); },
	},
}));
vi.mock("react-native", () => ({ BackHandler: { addEventListener: () => ({ remove: () => {} }) }, Keyboard: { addListener: () => ({ remove: () => {} }) } }));

import { useEffortSliderOpen, withPick } from "../useEffortSlider";
import { EFFORT_CONFIRM_MS, useChatEffort } from "./useChatEffort";

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
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); stored.clear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

function render(over: { snapshot?: ConversationSnapshot; disabled?: boolean; models?: ChatModel[]; onSettings?: () => Promise<void>; ultracodeKey?: string } = {}) {
	const onSettings = vi.fn(over.onSettings ?? (() => Promise.resolve()));
	const onOption = vi.fn(() => Promise.resolve([] as ChatConfigOption[]));
	// The slider's side as EffortSliderHost has it: subscribed to the
	// composer's switch on its own.
	function SliderHost({ chat }: { chat: ReturnType<typeof useChatEffort> }) {
		slider = useEffortSliderOpen(chat.choices, chat.busy, chat.commit, chat.slider);
		return null;
	}
	function Harness({ current }: { current: ConversationSnapshot }) {
		composerRenders += 1;
		effort = useChatEffort({ snapshot: current, models: over.models ?? models, options: [], disabled: over.disabled, onSettings, onOption, ultracodeKey: over.ultracodeKey });
		return <SliderHost chat={effort} />;
	}
	composerRenders = 0;
	act(() => { renderer = create(<Harness current={over.snapshot ?? snapshot()} />); });
	// The conversation reporting a new level.
	const report = (reasoningEffort: string) => act(() => renderer!.update(<Harness current={snapshot({ settings: { model: "opus", approvalMode: "auto", reasoningEffort } })} />));
	// The conversation reporting any snapshot.
	const update = (current: ConversationSnapshot) => act(() => renderer!.update(<Harness current={current} />));
	return { onSettings, onOption, report, update };
}

// The level the gauge and the slider show.
const shown = () => withPick(effort.choices, effort.slider.pick()).find((choice) => choice.selected)?.value;

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
	// The gauge and the slider show one level: the finger's, then the saved
	// one until the conversation reports it, so neither lags nor disagrees.
	describe("the level shown", () => {
		beforeEach(() => { vi.useFakeTimers(); });
		afterEach(() => { vi.useRealTimers(); });

		it("follows the finger, and holds the saved level until the conversation reports it", () => {
			const { report } = render();
			act(() => slider.toggle());
			act(() => effort.choose("medium"));
			expect(shown()).toBe("medium");
			act(() => effort.choose("high"));
			expect(shown()).toBe("high");
			act(() => effort.commit());
			expect(shown()).toBe("high");
			act(() => effort.close());
			expect(shown()).toBe("high");
			report("high");
			expect(effort.slider.pick()).toBeUndefined();
			expect(shown()).toBe("high");
		});

		it("keeps the saved level through a report that has not caught up", () => {
			const { report } = render();
			act(() => slider.toggle());
			act(() => { effort.choose("high"); effort.commit(); });
			report("medium");
			expect(shown()).toBe("high");
		});

		it("goes back to the conversation's level when the save fails", async () => {
			render({ onSettings: () => Promise.reject(new Error("offline")) });
			act(() => slider.toggle());
			act(() => { effort.choose("high"); effort.commit(); });
			expect(shown()).toBe("high");
			await act(async () => {});
			expect(shown()).toBe("low");
		});

		it("goes back to the conversation's level when it never reports the save", () => {
			render();
			act(() => slider.toggle());
			act(() => { effort.choose("high"); effort.commit(); });
			act(() => { vi.advanceTimersByTime(EFFORT_CONFIRM_MS - 1); });
			expect(shown()).toBe("high");
			act(() => { vi.advanceTimersByTime(1); });
			expect(shown()).toBe("low");
		});

		it("lets only the newest save drop the level, an older one failing late changing nothing", async () => {
			const fails: (() => void)[] = [];
			render({ onSettings: () => new Promise<void>((_resolve, reject) => { fails.push(() => reject(new Error("late"))); }) });
			act(() => slider.toggle());
			act(() => { effort.choose("medium"); effort.commit(); });
			act(() => { effort.choose("high"); effort.commit(); });
			await act(async () => { fails[0](); });
			expect(shown()).toBe("high");
		});

		it("does not drop the level under a finger still choosing", async () => {
			const fails: (() => void)[] = [];
			render({ onSettings: () => new Promise<void>((_resolve, reject) => { fails.push(() => reject(new Error("late"))); }) });
			act(() => slider.toggle());
			act(() => { effort.choose("high"); effort.commit(); });
			act(() => effort.choose("medium"));
			await act(async () => { fails[0](); });
			expect(shown()).toBe("medium");
		});

		it("lets go of the pick when let go on the level already saved", () => {
			render();
			act(() => slider.toggle());
			act(() => effort.choose("high"));
			act(() => { effort.choose("low"); effort.commit(); });
			expect(effort.slider.pick()).toBeUndefined();
			expect(shown()).toBe("low");
		});
	});

	// Claude Code's Ultracode, here the level above Extra High (a toggle on
	// desktop): Extra High saved, and asked for in each message while it is on.
	describe("ultracode", () => {
		const claude = [{ id: "opus", displayName: "Opus", efforts: ["low", "high", "xhigh", "max"], defaultEffort: "high" }] as ChatModel[];
		const claudeChat = (reasoningEffort?: string) => snapshot({ harness: "claude-code", settings: { model: "opus", approvalMode: "auto", reasoningEffort } });

		it("is the last level for Claude Code on a model with Extra High, and for no one else", () => {
			render({ models: claude, snapshot: claudeChat() });
			expect(effort.choices.map((choice) => choice.label)).toEqual(["Low", "High", "Xhigh", "Max", "Ultracode"]);
			render({ models: claude, snapshot: snapshot({ harness: "codex", settings: { model: "opus", approvalMode: "auto" } }) });
			expect(effort.choices.some((choice) => choice.value === "ultracode")).toBe(false);
		});

		// The message is prepared as the pick on its way has it, not only as the
		// conversation last reported; a failed save goes back to what was before.
		it("asks for Ultracode in the next message as soon as it is picked, until its save fails", async () => {
			const saves: Array<{ reject: (cause: Error) => void }> = [];
			const { update } = render({ models: claude, snapshot: claudeChat("high"), onSettings: () => new Promise<void>((_resolve, reject) => { saves.push({ reject }); }) });
			expect(effort.sendsUltracode()).toBe(false);
			act(() => slider.toggle());
			act(() => { effort.choose("ultracode"); effort.commit(); });
			expect(effort.sendsUltracode()).toBe(true);
			await act(async () => saves[0].reject(new Error("no")));
			expect(effort.sendsUltracode()).toBe(false);
			// On, reported, then another level on its way: off until it fails.
			act(() => { effort.choose("ultracode"); effort.commit(); });
			update(claudeChat("xhigh"));
			expect(effort.sendsUltracode()).toBe(true);
			act(() => { effort.choose("max"); effort.commit(); });
			expect(effort.sendsUltracode()).toBe(false);
			await act(async () => saves[2].reject(new Error("no")));
			expect(effort.sendsUltracode()).toBe(true);
		});

		// Two picks over each other, both failing: Ultracode goes back to how it
		// was before the first, not to the second's unconfirmed off.
		it("goes back to Ultracode when two picks made over it both fail", async () => {
			const saves: Array<{ resolve: () => void; reject: (cause: Error) => void }> = [];
			const { update } = render({ models: claude, snapshot: claudeChat("high"), onSettings: () => new Promise<void>((resolve, reject) => { saves.push({ resolve, reject }); }) });
			act(() => slider.toggle());
			act(() => { effort.choose("ultracode"); effort.commit(); });
			await act(async () => saves[0].resolve());
			update(claudeChat("xhigh"));
			expect(effort.sendsUltracode()).toBe(true);
			act(() => { effort.choose("high"); effort.commit(); });
			act(() => { effort.choose("low"); effort.commit(); });
			expect(effort.sendsUltracode()).toBe(false);
			await act(async () => saves[1].reject(new Error("no")));
			await act(async () => saves[2].reject(new Error("no")));
			expect(effort.sendsUltracode()).toBe(true);
			expect(effort.ultracode).toBe(true);
		});

		it("saves Extra High, and is on once the conversation reports it", () => {
			const onSettings = vi.fn(() => Promise.resolve());
			const { update } = render({ models: claude, snapshot: claudeChat(), onSettings });
			act(() => slider.toggle());
			act(() => { effort.choose("ultracode"); effort.commit(); });
			expect(onSettings).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ reasoningEffort: "xhigh" }));
			update(claudeChat("xhigh"));
			expect(effort.ultracode).toBe(true);
			expect(effort.choices.find((choice) => choice.selected)?.value).toBe("ultracode");
			// Another level turns it off.
			act(() => { effort.choose("max"); effort.commit(); });
			expect(effort.ultracode).toBe(false);
		});

		// Leaving the chat and coming back kept Extra High saved but dropped
		// the Ultracode prefix, with nothing to say so.
		it("lasts past the screen, kept for the conversation", async () => {
			const key = "ao.chat.ultracode.m.s";
			render({ models: claude, snapshot: claudeChat(), ultracodeKey: key });
			act(() => slider.toggle());
			act(() => { effort.choose("ultracode"); effort.commit(); });
			await act(async () => {});
			expect(stored.get(key)).toBe("1");
			act(() => renderer!.unmount());
			renderer = undefined;
			render({ models: claude, snapshot: claudeChat("xhigh"), ultracodeKey: key });
			await act(async () => {});
			expect(effort.ultracode).toBe(true);
			// Another conversation has its own.
			render({ models: claude, snapshot: claudeChat("xhigh"), ultracodeKey: "ao.chat.ultracode.m.other" });
			await act(async () => {});
			expect(effort.ultracode).toBe(false);
			// Turned off, it is forgotten.
			render({ models: claude, snapshot: claudeChat("xhigh"), ultracodeKey: key });
			await act(async () => {});
			act(() => slider.toggle());
			act(() => { effort.choose("max"); effort.commit(); });
			await act(async () => {});
			expect(stored.has(key)).toBe(false);
		});
	});

	// Fast mode rides on the slider as a bolt, where the model has it.
	it("flips fast mode at once, keeps it until the conversation reports it, and drops it if the save fails", async () => {
		const fastModels = [{ id: "opus", displayName: "Opus", efforts: ["low", "high"], supportsFast: true }] as ChatModel[];
		let fail = false;
		const { onSettings, update } = render({ models: fastModels, onSettings: () => (fail ? Promise.reject(new Error("no")) : Promise.resolve()) });
		expect(effort.fast?.on).toBe(false);
		act(() => effort.fast!.toggle());
		expect(effort.fast?.on).toBe(true);
		expect(onSettings).toHaveBeenLastCalledWith({ model: "opus", approvalMode: "auto", fastMode: true });
		update(snapshot({ settings: { model: "opus", approvalMode: "auto", fastMode: true } }));
		expect(effort.fast?.on).toBe(true);
		fail = true;
		await act(async () => effort.fast!.toggle());
		expect(effort.fast?.on).toBe(true);
		render();
		expect(effort.fast).toBeUndefined();
	});

	it("shows fast mode as the conversation has it again when a flip is never reported", () => {
		vi.useFakeTimers();
		const fastModels = [{ id: "opus", displayName: "Opus", efforts: ["low", "high"], supportsFast: true }] as ChatModel[];
		render({ models: fastModels, snapshot: snapshot({ settings: { model: "opus", approvalMode: "auto", fastMode: true } }) });
		act(() => effort.fast!.toggle());
		expect(effort.fast?.on).toBe(false);
		act(() => { vi.advanceTimersByTime(EFFORT_CONFIRM_MS); });
		expect(effort.fast?.on).toBe(true);
		vi.useRealTimers();
	});

	// An effort save replaces the whole set: a Fast flip the conversation has
	// not reported yet must ride along, or the save clears it.
	it("carries a Fast flip not yet reported into an effort save", () => {
		const fastModels = [{ id: "opus", displayName: "Opus", efforts: ["low", "high"], supportsFast: true }] as ChatModel[];
		const { onSettings } = render({ models: fastModels });
		act(() => effort.fast!.toggle());
		act(() => slider.toggle());
		act(() => { effort.choose("high"); effort.commit(); });
		expect(onSettings).toHaveBeenLastCalledWith({ model: "opus", approvalMode: "auto", fastMode: true, reasoningEffort: "high" });
	});

	// Picking the conversation's own level back while another is on its way
	// must save it, or the level on its way lands over it.
	it("saves the reported level again when it is picked back before another pick is reported", () => {
		const { onSettings } = render();
		act(() => slider.toggle());
		act(() => { effort.choose("high"); effort.commit(); });
		expect(onSettings).toHaveBeenCalledTimes(1);
		act(() => { effort.choose("low"); effort.commit(); });
		expect(onSettings).toHaveBeenCalledTimes(2);
		expect(onSettings).toHaveBeenLastCalledWith({ model: "opus", approvalMode: "auto", reasoningEffort: undefined });
	});

	it("sends nothing more, and keeps the hold, when the level on its way is picked again", () => {
		const { onSettings } = render();
		act(() => slider.toggle());
		act(() => { effort.choose("high"); effort.commit(); });
		act(() => { effort.choose("high"); effort.commit(); });
		expect(onSettings).toHaveBeenCalledTimes(1);
		expect(shown()).toBe("high");
	});

	// A Fast flip's save replaces the whole set too: an effort pick saved and
	// not yet reported must ride along, or the flip reverts it.
	it("carries an effort pick not yet reported into a Fast flip", () => {
		const fastModels = [{ id: "opus", displayName: "Opus", efforts: ["low", "high"], supportsFast: true }] as ChatModel[];
		const { onSettings, report } = render({ models: fastModels });
		act(() => slider.toggle());
		act(() => { effort.choose("high"); effort.commit(); });
		act(() => effort.fast!.toggle());
		expect(onSettings).toHaveBeenLastCalledWith({ model: "opus", approvalMode: "auto", reasoningEffort: "high", fastMode: true });
		// Once reported, the flip is built on the conversation's own again,
		// whatever it later changes to.
		report("high");
		report("low");
		act(() => effort.fast!.toggle());
		expect(onSettings).toHaveBeenLastCalledWith({ model: "opus", approvalMode: "auto", reasoningEffort: "low", fastMode: undefined });
	});

	it("carries a pending Ultracode into a Fast flip as the Extra High it saves", () => {
		const fastModels = [{ id: "opus", displayName: "Opus", efforts: ["low", "high", "xhigh"], defaultEffort: "high", supportsFast: true }] as ChatModel[];
		const { onSettings } = render({ models: fastModels });
		act(() => slider.toggle());
		act(() => { effort.choose("ultracode"); effort.commit(); });
		act(() => effort.fast!.toggle());
		expect(onSettings).toHaveBeenLastCalledWith({ model: "opus", approvalMode: "auto", reasoningEffort: "xhigh", fastMode: true });
	});

	it("keeps the newest Fast flip through the report of an older one", () => {
		const fastModels = [{ id: "opus", displayName: "Opus", efforts: ["low", "high"], supportsFast: true }] as ChatModel[];
		const { update } = render({ models: fastModels });
		act(() => effort.fast!.toggle());
		act(() => effort.fast!.toggle());
		expect(effort.fast?.on).toBe(false);
		// The first flip (on) is reported while the second (off) is on its way.
		update(snapshot({ settings: { model: "opus", approvalMode: "auto", fastMode: true } }));
		expect(effort.fast?.on).toBe(false);
		update(snapshot({ settings: { model: "opus", approvalMode: "auto" } }));
		expect(effort.fast?.on).toBe(false);
	});

	it("lets only the newest Fast flip's failure drop it", async () => {
		const fastModels = [{ id: "opus", displayName: "Opus", efforts: ["low", "high"], supportsFast: true }] as ChatModel[];
		const saves: { reject: (cause: Error) => void }[] = [];
		render({ models: fastModels, onSettings: () => new Promise<void>((_resolve, reject) => { saves.push({ reject }); }) });
		act(() => effort.fast!.toggle());
		act(() => effort.fast!.toggle());
		act(() => effort.fast!.toggle());
		expect(effort.fast?.on).toBe(true);
		await act(async () => saves[0].reject(new Error("offline")));
		expect(effort.fast?.on).toBe(true);
		await act(async () => saves[2].reject(new Error("offline")));
		expect(effort.fast?.on).toBe(false);
	});
});
