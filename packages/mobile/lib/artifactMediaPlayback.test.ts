import { afterEach, describe, expect, it, vi } from "vitest";
import { artifactMediaActiveScript, artifactMediaGateScript } from "./artifactMediaPlayback";

const platform = vi.hoisted(() => ({ OS: "ios" }));
vi.mock("react-native", () => ({ Platform: platform }));

afterEach(() => {
	platform.OS = "ios";
	vi.resetModules();
});

describe("ARTIFACT_MEDIA_PLAYBACK", () => {
	it("lets iOS play inline without a tap", async () => {
		const { ARTIFACT_MEDIA_PLAYBACK } = await import("./artifactMediaPlayback");
		expect(ARTIFACT_MEDIA_PLAYBACK).toEqual({ allowsInlineMediaPlayback: true, mediaPlaybackRequiresUserAction: false });
	});

	it("leaves Android on its defaults, which already autoplay muted video", async () => {
		platform.OS = "android";
		vi.resetModules();
		const { ARTIFACT_MEDIA_PLAYBACK } = await import("./artifactMediaPlayback");
		expect(ARTIFACT_MEDIA_PLAYBACK).toEqual({});
	});
});

type Listener = (event: { target?: unknown; source?: unknown; data?: unknown }) => void;
type Listeners = Map<string, Listener[]>;
const listen = (listeners: Listeners, name: string, listener: Listener) => listeners.set(name, [...(listeners.get(name) ?? []), listener]);

/** Just enough of a page (or a frame in one) for the gate: media elements, Audio, AudioContext, a document and a window. */
function fakePage(parent?: Record<string, unknown>) {
	const documentListeners: Listeners = new Map();
	const windowListeners: Listeners = new Map();
	const attached: FakeMedia[] = [];
	class FakeMedia {
		paused = true;
		plays = 0;
		listeners: Listeners = new Map();
		addEventListener(name: string, listener: Listener) { listen(this.listeners, name, listener); }
		play() { this.plays += 1; this.paused = false; this.fire("play"); return Promise.resolve(); }
		pause() { const was = !this.paused; this.paused = true; if (was) this.fire("pause"); }
		/** What the browser does when autoplay starts the element by itself. */
		autoplay() { this.paused = false; this.fire("play"); }
		end() { this.paused = true; this.fire("ended"); }
		fire(name: string) {
			for (const listener of this.listeners.get(name) ?? []) listener({ target: this });
			if (attached.includes(this)) for (const listener of documentListeners.get(name) ?? []) listener({ target: this });
		}
	}
	class FakeAudio extends FakeMedia {
		constructor(public src?: string) { super(); }
	}
	class FakeContext {
		state = "running";
		resumes = 0;
		listeners: Listeners = new Map();
		addEventListener(name: string, listener: Listener) { listen(this.listeners, name, listener); }
		suspend() { this.state = "suspended"; return Promise.resolve(); }
		resume() { this.resumes += 1; this.state = "running"; return Promise.resolve(); }
		close() { this.state = "closed"; for (const listener of this.listeners.get("statechange") ?? []) listener({}); }
	}
	const frames: Record<string, unknown>[] = [];
	const window: Record<string, unknown> = {
		HTMLMediaElement: FakeMedia, Audio: FakeAudio, AudioContext: FakeContext, frames,
		addEventListener: (name: string, listener: Listener) => listen(windowListeners, name, listener),
		// A message to this window, as postMessage delivers it (synchronously here).
		postMessage(data: unknown) { for (const listener of windowListeners.get("message") ?? []) listener({ source: parent, data }); },
	};
	window.parent = parent ?? window;
	const document = {
		addEventListener: (name: string, listener: Listener) => listen(documentListeners, name, listener),
		querySelectorAll: () => attached,
	};
	const run = (script: string) => new Function("window", "document", script)(window, document);
	const attach = () => { const el = new FakeMedia(); attached.push(el); return el; };
	/** A frame in this page, with the gate installed as the page's is. */
	const frame = (script: string) => { const child = fakePage(window); frames.push(child.window); child.run(script); return child; };
	return { window, run, attach, frame, FakeContext, Audio: () => window.Audio as new (src?: string) => FakeMedia, Context: () => window.AudioContext as new () => FakeContext };
}

describe("artifactMediaGateScript", () => {
	it("leaves the page on screen alone", async () => {
		const page = fakePage();
		page.run(artifactMediaGateScript(true));
		const video = page.attach();
		await video.play();
		expect(video.paused).toBe(false);
		expect(video.plays).toBe(1);
	});

	it("holds the media of a page that loads off screen, then starts it when shown", () => {
		const page = fakePage();
		page.run(artifactMediaGateScript(false));
		const video = page.attach();
		video.autoplay();
		const ding = new (page.Audio())("ding.mp3");
		void ding.play();
		const context = new (page.Context())();
		expect([video.paused, ding.paused, ding.plays, context.state]).toEqual([true, true, 0, "suspended"]);
		// A page asking to resume its context while off screen is held too.
		void context.resume();
		expect(context.resumes).toBe(0);

		page.run(artifactMediaActiveScript(true));
		expect([video.paused, ding.paused, context.state]).toEqual([false, false, "running"]);
	});

	it("pauses everything playing when the page goes off screen, detached audio included, and resumes only that", async () => {
		const page = fakePage();
		page.run(artifactMediaGateScript(true));
		const video = page.attach();
		const stopped = page.attach();
		const ding = new (page.Audio())("ding.mp3");
		const context = new (page.Context())();
		await video.play();
		await ding.play();

		page.run(artifactMediaActiveScript(false));
		expect([video.paused, ding.paused, context.state]).toEqual([true, true, "suspended"]);
		// Detached audio that autoplays off screen is caught by its own listener.
		const late = new (page.Audio())("late.mp3");
		late.autoplay();
		expect(late.paused).toBe(true);

		page.run(artifactMediaActiveScript(true));
		expect([video.paused, ding.paused, late.paused, context.state]).toEqual([false, false, false, "running"]);
		// An element that wasn't playing stays as it was.
		expect(stopped.paused).toBe(true);
	});

	it("lets go of media that finished, so it isn't started again", async () => {
		const page = fakePage();
		page.run(artifactMediaGateScript(true));
		const ding = new (page.Audio())("ding.mp3");
		await ding.play();
		ding.end();
		page.run(artifactMediaActiveScript(false));
		page.run(artifactMediaActiveScript(true));
		expect([ding.paused, ding.plays]).toEqual([true, 1]);
	});

	it("starts only the newest held sounds when a page comes back", () => {
		const page = fakePage();
		page.run(artifactMediaGateScript(false));
		const effects = Array.from({ length: 40 }, () => new (page.Audio())("pop.mp3"));
		for (const effect of effects) void effect.play();
		page.run(artifactMediaActiveScript(true));
		expect(effects.filter((effect) => !effect.paused)).toHaveLength(16);
		expect(effects.slice(-16).every((effect) => !effect.paused)).toBe(true);
	});

	it("passes the switch to the page's frames", () => {
		const page = fakePage();
		const script = artifactMediaGateScript(true);
		page.run(script);
		const frame = page.frame(script);
		const video = frame.attach();
		void video.play();

		page.run(artifactMediaActiveScript(false));
		expect(video.paused).toBe(true);
		page.run(artifactMediaActiveScript(true));
		expect(video.paused).toBe(false);
	});

	it("takes the switch only from the page that holds the frame", () => {
		const page = fakePage();
		const frame = page.frame(artifactMediaGateScript(true));
		const video = frame.attach();
		void video.play();
		// A message from anyone but the frame's parent (here, a parent the frame no longer has).
		(frame.window as { parent: unknown }).parent = {};
		(frame.window as { postMessage(data: unknown): void }).postMessage({ __aoMediaActive: false });
		expect(video.paused).toBe(false);
	});

	it("keeps a subclass's own methods", () => {
		const page = fakePage();
		page.run(artifactMediaGateScript(true));
		const Context = page.Context();
		class Synth extends Context { note() { return "beep"; } }
		const synth = new Synth();
		expect(synth.note()).toBe("beep");
		expect(synth).toBeInstanceOf(page.FakeContext);
		class Effect extends page.Audio() { kind() { return "effect"; } }
		expect(new Effect("pop.mp3").kind()).toBe("effect");
	});

	it("stops holding a context once the page closes it", () => {
		const page = fakePage();
		page.run(artifactMediaGateScript(true));
		const context = new (page.Context())();
		context.close();
		page.run(artifactMediaActiveScript(false));
		page.run(artifactMediaActiveScript(true));
		expect([context.state, context.resumes]).toEqual(["closed", 0]);
	});

	it("installs once", () => {
		const page = fakePage();
		page.run(artifactMediaGateScript(true));
		const play = (page.window.HTMLMediaElement as { prototype: { play: unknown } }).prototype.play;
		page.run(artifactMediaGateScript(false));
		expect((page.window.HTMLMediaElement as { prototype: { play: unknown } }).prototype.play).toBe(play);
	});
});
