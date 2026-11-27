import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PendingAgentSwitch } from "../agentSwitch";
import type { ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";

vi.mock("react-native", () => ({ BackHandler: { addEventListener: () => ({ remove: () => {} }) }, Keyboard: { addListener: () => ({ remove: () => {} }) } }));

import { useChatModelWheel } from "./useChatModelWheel";

// Fast mode is off in the app for now (fastMode.ts); its behaviour is still tested.
vi.mock("./fastMode", () => ({ FAST_MODE_SHOWN: true }));

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
	{ id: "opus", displayName: "Opus", efforts: ["low", "high"], defaultEffort: "low" },
	{ id: "haiku", displayName: "Haiku" },
] as ChatModel[];
const codex = { id: "codex", label: "Codex" };

let renderer: ReactTestRenderer | undefined;
let wheel: ReturnType<typeof useChatModelWheel>;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

function render(pending?: PendingAgentSwitch, over: { fastOnEffort?: boolean } = {}) {
	const onSettings = vi.fn(() => Promise.resolve());
	const onOption = vi.fn(() => Promise.resolve([] as ChatConfigOption[]));
	const onPick = vi.fn();
	function Harness() {
		wheel = useChatModelWheel({
			snapshot: snapshot(),
			models: over.fastOnEffort === undefined ? models : [{ ...models[0], supportsFast: true }, models[1]],
			fastOnEffort: over.fastOnEffort,
			options: [],
			onSettings,
			onOption,
			otherAgents: { agents: [codex], models: { codex: { models: [{ id: "fable-5-1", label: "Fable 5.1" }], older: [{ id: "fable-5", label: "Fable 5" }], total: 2, defaultLabel: "Fable 5.1" } }, pending, onPick },
		});
		return null;
	}
	act(() => { renderer = create(<Harness />); });
	return { onSettings, onPick };
}

describe("chat model wheel", () => {
	// Two wheels, as iOS's date picker: the agent, then its models.
	it("lists the running agent first with its models, then the others with a default first and older last", () => {
		render();
		expect(wheel.agents.map((agent) => agent.label)).toEqual(["Claude", "Codex"]);
		expect(wheel.agents[0].models.map((model) => model.label)).toEqual(["Opus", "Haiku"]);
		// Another agent's default is starred where it is listed, not listed twice.
		expect(wheel.agents[1].models.map((model) => [model.label, Boolean(model.starred)])).toEqual([["Fable 5.1", true], ["Fable 5", false]]);
		expect(wheel.chosenAgent).toBe("claude-code");
		expect(wheel.chosenModel).toBe("opus");
	});

	it("saves the running agent's model where the wheel rests, and nothing for the one already in effect", () => {
		const { onSettings, onPick } = render();
		act(() => wheel.choose("claude-code", "opus"));
		expect(onSettings).not.toHaveBeenCalled();
		act(() => wheel.choose("claude-code", "haiku"));
		expect(onSettings).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ model: "haiku" }));
		expect(onPick).not.toHaveBeenCalled();
	});

	// Another agent's model only records the switch, which runs with the next message.
	it("records a switch to another agent's model", () => {
		const { onSettings, onPick } = render();
		act(() => wheel.choose("codex", "fable-5"));
		expect(onPick).toHaveBeenLastCalledWith({ harness: "codex", model: "fable-5", modelLabel: "Fable 5" });
		act(() => wheel.choose("codex", wheel.agents[1].models[0].key));
		expect(onPick).toHaveBeenLastCalledWith({ harness: "codex", model: "fable-5-1", modelLabel: "Fable 5.1" });
		expect(onSettings).not.toHaveBeenCalled();
	});

	it("stands on the switch picked, and drops it when the running agent is chosen again", () => {
		const { onPick } = render({ harness: "codex", model: "fable-5", modelLabel: "Fable 5" });
		expect(wheel.chosenAgent).toBe("codex");
		expect(wheel.chosenModel).toBe("fable-5");
		// What the chat runs now, for the wheels to return to.
		expect(wheel.runningAgent).toBe("claude-code");
		expect(wheel.runningModel).toBe("opus");
		act(() => wheel.choose("codex", "fable-5"));
		expect(onPick).not.toHaveBeenCalled();
		act(() => wheel.choose("claude-code", "opus"));
		expect(onPick).toHaveBeenCalledExactlyOnceWith(undefined);
	});

	it("stands a switch to an agent's own default on its starred model", () => {
		render({ harness: "codex", modelLabel: "Fable 5.1" });
		expect(wheel.chosenAgent).toBe("codex");
		expect(wheel.chosenModel).toBe("fable-5-1");
	});

	// Rolled away and back, the wheels close on the starred model the switch
	// stands on: saving it would pin the model and stop following the default.
	it("keeps a switch to an agent's own default when the wheels close on its starred model", () => {
		const { onPick } = render({ harness: "codex", modelLabel: "Fable 5.1" });
		act(() => wheel.choose("codex", "fable-5-1"));
		expect(onPick).not.toHaveBeenCalled();
		// Another of its models is still a change.
		act(() => wheel.choose("codex", "fable-5"));
		expect(onPick).toHaveBeenCalledExactlyOnceWith({ harness: "codex", model: "fable-5", modelLabel: "Fable 5" });
	});

	// Where the effort slider shows, Fast mode is its bolt and not a row here.
	it("lists Fast mode under the wheels only while the effort slider does not carry it", () => {
		render(undefined, { fastOnEffort: false });
		expect(wheel.settings.map((setting) => setting.label)).toEqual(["Fast mode"]);
		render(undefined, { fastOnEffort: true });
		expect(wheel.settings).toEqual([]);
	});
});
