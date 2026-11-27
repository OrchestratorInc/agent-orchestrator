import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, __DEV__: false });
const fixtures = vi.hoisted(() => ({
	appState: new Set<(next: string) => void>(),
	openSettings: vi.fn(async () => {}),
}));
vi.mock("react-native", () => ({
	AppState: {
		addEventListener: (_event: string, listener: (next: string) => void) => {
			fixtures.appState.add(listener);
			return { remove: () => fixtures.appState.delete(listener) };
		},
	},
	Linking: { openSettings: fixtures.openSettings },
}));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("./deviceProvider", () => ({ getDeviceVoiceProvider: () => { throw new Error("tests inject a provider"); } }));

import { activateKeepAwakeAsync, deactivateKeepAwake } from "expo-keep-awake";
import type { VoiceCallbacks, VoiceMode, VoiceProvider } from "./types";
import { DENIED_MESSAGE, DENIED_NOTICE_MS, useVoiceInput, type UseVoiceInput } from "./useVoiceInput";

function fakeProvider() {
	const started: { callbacks: VoiceCallbacks; mode?: VoiceMode }[] = [];
	const provider: VoiceProvider = {
		id: "device",
		available: () => true,
		language: () => "en-US",
		requestPermission: vi.fn(async () => true),
		checkPermission: vi.fn(async () => true),
		start: vi.fn(async (callbacks: VoiceCallbacks, opts?: { mode?: VoiceMode }) => { started.push({ callbacks, mode: opts?.mode }); }),
		stop: vi.fn(),
		abort: vi.fn(),
	};
	return { provider, started };
}

let renderer: ReactTestRenderer | undefined;
afterEach(() => { if (renderer) act(() => renderer!.unmount()); renderer = undefined; vi.clearAllMocks(); });

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

// The key taps for every press, so the double tap that latches must not tap
// again from here; the next buzz is the one that says the mic is listening.
it("latches on a double tap without a second haptic of its own", async () => {
	vi.useFakeTimers();
	try {
		const { haptics } = await import("../haptics");
		const { provider, started } = fakeProvider();
		const voice = mount(provider);
		await act(async () => voice.current.pressIn());
		act(() => voice.current.pressOut());
		act(() => voice.current.pressIn());
		await act(async () => { vi.advanceTimersByTime(200); });
		// The first tap's push recording is thrown away, and the latched one follows.
		expect(started.map((entry) => entry.mode)).toEqual(["push", "latched"]);
		expect(provider.abort).toHaveBeenCalledOnce();
		expect(haptics.tap).not.toHaveBeenCalled();
		act(() => started[1].callbacks.onReady());
		expect(haptics.select).toHaveBeenCalledOnce();
	} finally {
		vi.useRealTimers();
	}
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

// The waveform reads the level through a subscription, so the composer holding
// the hook does not redraw several times a second.
it("streams the input level only while capturing, and drops to silence when it ends", async () => {
	const { provider, started } = fakeProvider();
	const voice = mount(provider);
	const heard: number[] = [];
	const unsubscribe = voice.current.subscribeLevel((level) => heard.push(level));

	await act(async () => voice.current.toggleLatched());
	// Still warming up: nothing is capturing, so nothing is drawn.
	act(() => started[0].callbacks.onLevel?.(0.4));
	expect(heard).toEqual([]);
	act(() => started[0].callbacks.onReady());
	act(() => started[0].callbacks.onLevel?.(0.6));
	act(() => started[0].callbacks.onFinal("done"));
	expect(heard).toEqual([0.6, 0]);

	unsubscribe();
	await act(async () => voice.current.toggleLatched());
	act(() => started[1].callbacks.onReady());
	act(() => started[1].callbacks.onLevel?.(0.9));
	expect(heard).toEqual([0.6, 0]);
});

// Leaving the app and coming back, as a trip to Settings does.
async function leaveAndReturn() {
	await act(async () => {
		for (const listener of [...fixtures.appState]) listener("background");
		for (const listener of [...fixtures.appState]) listener("active");
		await Promise.resolve();
	});
}

function deniedProvider() {
	const fake = fakeProvider();
	vi.mocked(fake.provider.requestPermission).mockResolvedValue(false);
	vi.mocked(fake.provider.checkPermission).mockResolvedValue(false);
	return fake;
}

describe("when microphone access is off", () => {
	beforeEach(() => { vi.useFakeTimers(); });
	afterEach(() => { vi.useRealTimers(); });

	// It says so once, then gets out of the way: the crossed-out mic keeps saying
	// it, and a message pinned under every later turn was noise.
	it("says so once, then lets the notice go", async () => {
		const { provider, started } = deniedProvider();
		const voice = mount(provider);
		await act(async () => voice.current.toggleLatched());
		expect(voice.current.state).toBe("denied");
		expect(voice.current.error).toBe(DENIED_MESSAGE);
		expect(started).toHaveLength(0);

		act(() => { vi.advanceTimersByTime(DENIED_NOTICE_MS - 1); });
		expect(voice.current.error).toBe(DENIED_MESSAGE);
		act(() => { vi.advanceTimersByTime(1); });
		expect(voice.current.error).toBeNull();
		expect(voice.current.state).toBe("denied");
	});

	it("opens Settings from the mic instead of asking again", async () => {
		const { provider } = deniedProvider();
		const voice = mount(provider);
		await act(async () => voice.current.toggleLatched());

		act(() => voice.current.toggleLatched());
		expect(fixtures.openSettings).toHaveBeenCalledOnce();
		expect(provider.requestPermission).toHaveBeenCalledOnce();
		// The notice is gone while the user is off in Settings.
		expect(voice.current.error).toBeNull();

		// The older hold gesture goes to Settings too, and its release changes nothing.
		act(() => voice.current.pressIn());
		act(() => voice.current.pressOut());
		expect(fixtures.openSettings).toHaveBeenCalledTimes(2);
		expect(voice.current.state).toBe("denied");
		expect(provider.abort).not.toHaveBeenCalled();
	});

	it("picks the mic back up once access is turned on in Settings", async () => {
		const { provider, started } = deniedProvider();
		const voice = mount(provider);
		await act(async () => voice.current.toggleLatched());
		act(() => voice.current.toggleLatched());

		vi.mocked(provider.checkPermission).mockResolvedValue(true);
		vi.mocked(provider.requestPermission).mockResolvedValue(true);
		await leaveAndReturn();
		expect(voice.current.state).toBe("idle");
		expect(voice.current.error).toBeNull();

		await act(async () => voice.current.toggleLatched());
		expect(started).toHaveLength(1);
		expect(fixtures.openSettings).toHaveBeenCalledOnce();
	});

	// Even with the notice still showing, turning access on clears it at once.
	it("clears a notice that is still up when access comes back", async () => {
		const { provider } = deniedProvider();
		const voice = mount(provider);
		await act(async () => voice.current.toggleLatched());
		expect(voice.current.error).toBe(DENIED_MESSAGE);

		vi.mocked(provider.checkPermission).mockResolvedValue(true);
		await leaveAndReturn();
		expect(voice.current.error).toBeNull();
		expect(voice.current.state).toBe("idle");
	});

	it("says it again only on coming back from Settings with access still off", async () => {
		const { provider } = deniedProvider();
		const voice = mount(provider);
		await act(async () => voice.current.toggleLatched());
		act(() => { vi.advanceTimersByTime(DENIED_NOTICE_MS); });

		// Switching apps for some other reason stays quiet.
		await leaveAndReturn();
		expect(provider.checkPermission).toHaveBeenCalledOnce();
		expect(voice.current.error).toBeNull();

		act(() => voice.current.toggleLatched());
		await leaveAndReturn();
		expect(voice.current.state).toBe("denied");
		expect(voice.current.error).toBe(DENIED_MESSAGE);
		act(() => { vi.advanceTimersByTime(DENIED_NOTICE_MS); });
		expect(voice.current.error).toBeNull();

		// That trip is used up: the next unrelated return is quiet again.
		await leaveAndReturn();
		expect(voice.current.error).toBeNull();
	});

	it("keeps the notice up when Settings cannot be opened", async () => {
		const { provider } = deniedProvider();
		const voice = mount(provider);
		await act(async () => voice.current.toggleLatched());
		act(() => { vi.advanceTimersByTime(DENIED_NOTICE_MS); });

		fixtures.openSettings.mockRejectedValueOnce(new Error("no settings"));
		await act(async () => { voice.current.toggleLatched(); await Promise.resolve(); });
		expect(voice.current.error).toBe(DENIED_MESSAGE);
	});

	it("leaves a granted mic alone when the app comes back", async () => {
		const { provider } = fakeProvider();
		const voice = mount(provider);
		await leaveAndReturn();
		expect(provider.checkPermission).not.toHaveBeenCalled();
		expect(voice.current.state).toBe("idle");
	});
});

// The OS idle timer only resets on touch, so hands-free dictation would lock the
// screen mid-sentence, and the lock backgrounds the app and aborts the mic.
it("keeps the screen awake only while the mic is open", async () => {
	const { provider, started } = fakeProvider();
	const onTranscript = vi.fn();
	const voice = mount(provider, onTranscript);
	expect(activateKeepAwakeAsync).not.toHaveBeenCalled();

	await act(async () => voice.current.toggleLatched());
	expect(activateKeepAwakeAsync).toHaveBeenCalledExactlyOnceWith("dictation");
	// Warming up to listening is one hold, not a release and a fresh one.
	act(() => started[0].callbacks.onReady());
	expect(activateKeepAwakeAsync).toHaveBeenCalledOnce();
	expect(deactivateKeepAwake).not.toHaveBeenCalled();

	// Stopping waits for the final result, and the screen stays awake until it lands.
	act(() => voice.current.toggleLatched());
	expect(deactivateKeepAwake).not.toHaveBeenCalled();
	act(() => started[0].callbacks.onFinal("ship it"));
	expect(deactivateKeepAwake).toHaveBeenCalledExactlyOnceWith("dictation");
	expect(onTranscript).toHaveBeenCalledWith("ship it");
});

it("releases the screen when dictation fails", async () => {
	const { provider, started } = fakeProvider();
	const voice = mount(provider);
	await act(async () => voice.current.toggleLatched());
	act(() => started[0].callbacks.onReady());
	act(() => started[0].callbacks.onError("Recogniser went away"));
	expect(voice.current.state).toBe("idle");
	expect(deactivateKeepAwake).toHaveBeenCalledExactlyOnceWith("dictation");
});

it("releases the screen when the app leaves the foreground mid-dictation", async () => {
	const { provider, started } = fakeProvider();
	const voice = mount(provider);
	await act(async () => voice.current.toggleLatched());
	act(() => started[0].callbacks.onReady());
	act(() => { for (const listener of [...fixtures.appState]) listener("background"); });
	expect(provider.abort).toHaveBeenCalledOnce();
	expect(deactivateKeepAwake).toHaveBeenCalledExactlyOnceWith("dictation");
});

it("releases the screen when the composer unmounts mid-dictation", async () => {
	const { provider, started } = fakeProvider();
	const voice = mount(provider);
	await act(async () => voice.current.toggleLatched());
	act(() => started[0].callbacks.onReady());
	act(() => renderer!.unmount());
	renderer = undefined;
	expect(deactivateKeepAwake).toHaveBeenCalledExactlyOnceWith("dictation");
});
