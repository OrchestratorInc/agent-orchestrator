import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, __DEV__: false });
vi.mock("react-native", () => ({ AppState: { addEventListener: () => ({ remove: () => {} }) } }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("./deviceProvider", () => ({ getDeviceVoiceProvider: () => { throw new Error("tests inject a provider"); } }));

import type { VoiceCallbacks, VoiceMode, VoiceProvider } from "./types";
import { useVoiceInput, type UseVoiceInput } from "./useVoiceInput";

function fakeProvider() {
	const started: { callbacks: VoiceCallbacks; mode?: VoiceMode }[] = [];
	const provider: VoiceProvider = {
		id: "device",
		available: () => true,
		language: () => "en-US",
		requestPermission: vi.fn(async () => true),
		start: vi.fn(async (callbacks: VoiceCallbacks, opts?: { mode?: VoiceMode }) => { started.push({ callbacks, mode: opts?.mode }); }),
		stop: vi.fn(),
		abort: vi.fn(),
	};
	return { provider, started };
}

let renderer: ReactTestRenderer | undefined;
afterEach(() => { if (renderer) act(() => renderer!.unmount()); renderer = undefined; });

function mount(provider: VoiceProvider, onTranscript = vi.fn()) {
	const voice: { current?: UseVoiceInput } = {};
	function Harness() {
		voice.current = useVoiceInput({ onTranscript, provider });
		return null;
	}
	act(() => { renderer = create(<Harness />); });
	return voice as { current: UseVoiceInput };
}

// A screen reader activates the mic instead of holding it, so one activation
// has to start hands-free dictation and the next has to finish it.
it("latches hands-free dictation on one activation and finishes it on the next", async () => {
	const { provider, started } = fakeProvider();
	const onTranscript = vi.fn();
	const voice = mount(provider, onTranscript);

	await act(async () => voice.current.toggleLatched());
	expect(started).toHaveLength(1);
	expect(started[0].mode).toBe("latched");
	act(() => started[0].callbacks.onReady());
	expect(voice.current.state).toBe("recording");
	expect(voice.current.mode).toBe("latched");

	act(() => voice.current.toggleLatched());
	expect(provider.stop).toHaveBeenCalledOnce();
	act(() => started[0].callbacks.onFinal("ship it"));
	expect(onTranscript).toHaveBeenCalledWith("ship it");
	expect(voice.current.state).toBe("idle");
});

it("drops a latched start that is still warming up when activated again", async () => {
	const { provider, started } = fakeProvider();
	const voice = mount(provider);
	await act(async () => voice.current.toggleLatched());
	expect(voice.current.state).toBe("starting");
	act(() => voice.current.toggleLatched());
	expect(provider.abort).toHaveBeenCalledOnce();
	expect(provider.stop).not.toHaveBeenCalled();
	expect(voice.current.state).toBe("idle");
	expect(started).toHaveLength(1);
});
