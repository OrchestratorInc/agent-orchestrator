import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const env = vi.hoisted(() => ({
	os: "android",
	listeners: new Set<(state: string) => void>(),
	height: { modify: vi.fn() },
	progress: { modify: vi.fn() },
}));
vi.mock("react-native", () => ({
	Platform: { get OS() { return env.os; } },
	AppState: {
		addEventListener: (_: string, listener: (state: string) => void) => {
			env.listeners.add(listener);
			return { remove: () => env.listeners.delete(listener) };
		},
	},
}));
vi.mock("react-native-keyboard-controller", () => ({ useReanimatedKeyboardAnimation: () => ({ height: env.height, progress: env.progress }) }));

import { KeyboardResumeResync } from "./KeyboardResumeResync";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	env.os = "android";
	env.listeners.clear();
	env.height.modify.mockClear();
	env.progress.modify.mockClear();
});
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

function mount() {
	act(() => { renderer = create(<KeyboardResumeResync />); });
}
function appState(state: string) {
	act(() => env.listeners.forEach((listener) => listener(state)));
}

// A keyboard that closed while the app was paused left the chat composer drawn
// at its keyboard-open height on resume, though every value said closed.
it("forces the keyboard's values through again when the app comes back", () => {
	mount();
	appState("background");
	expect(env.height.modify).not.toHaveBeenCalled();
	appState("active");
	// No modifier: modify() forces the update, where a same-value write is dropped.
	expect(env.height.modify).toHaveBeenCalledWith();
	expect(env.progress.modify).toHaveBeenCalledWith();
});

it("does nothing on iOS", () => {
	env.os = "ios";
	mount();
	appState("active");
	expect(env.listeners.size).toBe(0);
	expect(env.height.modify).not.toHaveBeenCalled();
});

it("stops listening once unmounted", () => {
	mount();
	act(() => renderer?.unmount());
	renderer = undefined;
	expect(env.listeners.size).toBe(0);
});
