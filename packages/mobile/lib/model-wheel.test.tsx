import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }), focused: { current: null as { focus: () => void } | null } }));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		BackHandler: { addEventListener: () => ({ remove: () => {} }) },
		Keyboard: { addListener: () => ({ remove: () => {} }) },
		Platform: { OS: "ios" },
		TextInput: { State: { currentlyFocusedInput: () => fixtures.focused.current } },
		ScrollView: host("ScrollView"),
		StyleSheet: { create: (styles: object) => styles },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("react-native-gesture-handler", async () => {
	const React = await import("react");
	return { Pressable: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Pressable", props, children) };
});
vi.mock("./composer-overlay", async () => {
	const React = await import("react");
	return { COMPOSER_CONTROL_RISE: 37, ComposerOverlay: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("ComposerOverlay", props, children) };
});
vi.mock("./AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./model-search", () => ({ ModelSearchSheet: "ModelSearchSheet" }));
vi.mock("./effort-slider-host", () => ({ useWarmAfterIdle: () => true }));
vi.mock("./haptics", () => ({ haptics: { select: () => {} } }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));
vi.mock("./model-list", () => ({ ModelList: "ModelList", LIST_ROW: 46, LIST_ROWS: 6 }));

import type { WheelAgent } from "./chat/useChatModelWheel";
import { ModelWheelHost } from "./model-wheel";
import { createEffortSliderSwitch } from "./useEffortSlider";

const agents: WheelAgent[] = [
	{ id: "claude-code", label: "Claude", models: [{ key: "opus", label: "Opus", starred: true }, { key: "sonnet", label: "Sonnet" }, { key: "haiku", label: "Haiku" }] },
	{ id: "codex", label: "Codex", models: [{ key: "gpt-old", label: "GPT old" }, { key: "gpt", label: "GPT", starred: true }] },
];

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");

function render(over: { chosenAgent?: string; chosenModel?: string; agentsLocked?: boolean } = {}) {
	const slider = createEffortSliderSwitch();
	const choose = vi.fn();
	const element = (props: { chosenAgent?: string; chosenModel?: string; agentsLocked?: boolean }) => (
		<ModelWheelHost
			slider={slider}
			busy={false}
			agents={agents}
			chosenAgent={props.chosenAgent ?? "claude-code"}
			chosenModel={props.chosenModel ?? "opus"}
			runningAgent="claude-code"
			runningModel="opus"
			settings={[]}
			agentsLocked={props.agentsLocked ?? false}
			choose={choose}
			toggleSetting={() => {}}
		/>
	);
	act(() => { renderer = create(element(over)); });
	act(() => slider.setOpen(true));
	const wheel = (testID: string) => renderer!.root.findAll((node) => hostType(node) === "ModelList" && node.props.testID === testID)[0];
	return {
		slider,
		choose,
		agentButton: (id: string) => renderer!.root.findAll((node) => hostType(node) === "Pressable" && node.props.testID === `model-wheel-agent-${id}`)[0],
		pickAgent: (id: string) => act(() => renderer!.root.findAll((node) => hostType(node) === "Pressable" && node.props.testID === `model-wheel-agent-${id}`)[0].props.onPress()),
		modelsWheel: () => wheel("model-wheel-models"),
		pick: (index: number) => act(() => wheel("model-wheel-models").props.onPick(index)),
		rerender: (props: { chosenAgent?: string; chosenModel?: string }) => act(() => renderer!.update(element(props))),
		openSearch: () => act(() => renderer!.root.findAll((node) => hostType(node) === "Pressable" && node.props.testID === "model-wheel-search")[0].props.onPress()),
		search: () => renderer!.root.findAll((node) => hostType(node) === "ModelSearchSheet")[0],
	};
}

describe("model wheel", () => {
	// A plain list: a tap is a choice, made once, and puts the list away.
	it("chooses the model tapped, once, and puts the list away", () => {
		const { slider, choose, pick } = render();
		pick(2);
		expect(slider.isOpen()).toBe(false);
		expect(choose).toHaveBeenCalledExactlyOnceWith("claude-code", "haiku");
	});

	it("looks at another agent without switching to it, and hands back the pair in force when rolled back", () => {
		const { slider, choose, pickAgent, modelsWheel } = render();
		pickAgent("codex");
		// Another agent starts on its starred default, not its first model.
		expect(modelsWheel().props.items[modelsWheel().props.index].key).toBe("gpt");
		pickAgent("claude-code");
		expect(modelsWheel().props.items[modelsWheel().props.index].key).toBe("opus");
		act(() => slider.setOpen(false));
		// `choose` leaves the pair already in force alone.
		expect(choose).toHaveBeenCalledExactlyOnceWith("claude-code", "opus");
	});

	it("chooses nothing when the list closes untouched, and opens where the chat stands next time", () => {
		const { slider, choose, pick, modelsWheel } = render();
		act(() => slider.setOpen(false));
		expect(choose).not.toHaveBeenCalled();
		act(() => slider.setOpen(true));
		pick(1);
		act(() => slider.setOpen(true));
		// The save is still on its way: the wheels show the chat's model.
		expect(modelsWheel().props.index).toBe(0);
	});

	// A tap landing as the list goes is no choice.
	it("drops a pick that lands after the list closes", () => {
		const { slider, choose, pick, modelsWheel } = render();
		act(() => slider.setOpen(false));
		pick(1);
		expect(modelsWheel().props.index).toBe(0);
		act(() => slider.setOpen(true));
		act(() => slider.setOpen(false));
		expect(choose).not.toHaveBeenCalled();
	});

	// With a switch to Codex picked, rolling back to Claude returns to the
	// model Claude runs, not Claude's first listed one.
	it("returns the running agent to its own model while a switch is picked", () => {
		const { slider, choose, pickAgent, modelsWheel } = render({ chosenAgent: "codex", chosenModel: "gpt-old" });
		expect(modelsWheel().props.items[modelsWheel().props.index].key).toBe("gpt-old");
		pickAgent("claude-code");
		act(() => slider.setOpen(false));
		expect(choose).toHaveBeenCalledExactlyOnceWith("claude-code", "opus");
	});

	// A request card replacing the composer takes the host with it.
	it("closes its switch, and chooses where it stood, when the host goes away", () => {
		const { slider, choose, pickAgent } = render();
		pickAgent("codex");
		act(() => renderer!.update(<></>));
		expect(slider.isOpen()).toBe(false);
		expect(choose).toHaveBeenCalledExactlyOnceWith("codex", "gpt");
	});

	// Each agent's list opens on its own model in force.
	it("rebuilds the model list for each agent", () => {
		const { pickAgent, modelsWheel } = render();
		const before = modelsWheel();
		pickAgent("codex");
		expect(modelsWheel()).not.toBe(before);
		expect(modelsWheel().props.accessibilityLabel).toBe("Model");
	});

	// Switching agent is one tap on its button, not a turn of a second wheel.
	it("lists the agents as buttons, the one the wheel shows selected", () => {
		const { agentButton, pickAgent } = render();
		expect(agentButton("claude-code").props.accessibilityState).toEqual({ selected: true, disabled: false });
		expect(agentButton("codex").props.accessibilityState).toEqual({ selected: false, disabled: false });
		pickAgent("codex");
		expect(agentButton("codex").props.accessibilityState).toEqual({ selected: true, disabled: false });
		expect(agentButton("claude-code").props.accessibilityState).toEqual({ selected: false, disabled: false });
	});

	// A switch waits until the agent is idle: the other agents are held still.
	it("holds the other agents' buttons while the agent is working", () => {
		const { agentButton } = render({ agentsLocked: true });
		expect(agentButton("codex").props.disabled).toBe(true);
		expect(agentButton("claude-code").props.disabled).toBe(false);
	});

	// The search row opens a page of its own, typing at once: the panel goes.
	it("opens the search in place of the panel, and drops an agent only looked at", () => {
		const { slider, choose, pickAgent, openSearch, search } = render();
		expect(search().props.visible).toBe(false);
		pickAgent("codex");
		openSearch();
		expect(slider.isOpen()).toBe(false);
		expect(search().props.visible).toBe(true);
		// The agent tapped on the way to the search is no choice.
		expect(choose).not.toHaveBeenCalled();
	});

	it("chooses what the search picks and gives the field the keys back once it has gone", () => {
		const field = { focus: vi.fn() };
		fixtures.focused.current = field;
		const { choose, openSearch, search } = render();
		openSearch();
		act(() => search().props.onPick("codex", "gpt-old"));
		expect(choose).toHaveBeenCalledExactlyOnceWith("codex", "gpt-old");
		expect(search().props.visible).toBe(false);
		expect(field.focus).not.toHaveBeenCalled();
		act(() => search().props.onDismissed());
		expect(field.focus).toHaveBeenCalledOnce();
		fixtures.focused.current = null;
	});

	it("chooses nothing when the search is cancelled", () => {
		const { choose, openSearch, search } = render();
		openSearch();
		act(() => search().props.onClose());
		expect(search().props.visible).toBe(false);
		expect(choose).not.toHaveBeenCalled();
	});
});
