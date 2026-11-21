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

type Listener = (event: { target: unknown }) => void;

/** Just enough of a page for the gate: media elements, Audio, AudioContext, and a document. */
function fakePage() {
	const documentListeners: Listener[] = [];
	const attached: FakeMedia[] = [];
	class FakeMedia {
		paused = true;
		plays = 0;
		listeners: Listener[] = [];
		addEventListener(_name: string, listener: Listener) { this.listeners.push(listener); }
		play() { this.plays += 1; this.paused = false; this.fire(); return Promise.resolve(); }
		pause() { this.paused = true; }
		/** What the browser does when autoplay starts the element by itself. */
		autoplay() { this.paused = false; this.fire(); }
		fire() {
			for (const listener of this.listeners) listener({ target: this });
			if (attached.includes(this)) for (const listener of documentListeners) listener({ target: this });
		}
	}
	class FakeAudio extends FakeMedia {
		constructor(public src?: string) { super(); }
	}
	class FakeContext {
		state = "running";
		resumes = 0;
		suspend() { this.state = "suspended"; return Promise.resolve(); }
		resume() { this.resumes += 1; this.state = "running"; return Promise.resolve(); }
	}
	const window: Record<string, unknown> = { HTMLMediaElement: FakeMedia, Audio: FakeAudio, AudioContext: FakeContext };
	const document = {
		addEventListener: (_name: string, listener: Listener) => documentListeners.push(listener),
		querySelectorAll: () => attached,
	};
	const run = (script: string) => new Function("window", "document", script)(window, document);
	const attach = () => { const el = new FakeMedia(); attached.push(el); return el; };
	return { window, run, attach, Audio: () => window.Audio as new (src?: string) => FakeMedia, Context: () => window.AudioContext as new () => FakeContext };
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

	it("installs once", () => {
		const page = fakePage();
		page.run(artifactMediaGateScript(true));
		const play = (page.window.HTMLMediaElement as { prototype: { play: unknown } }).prototype.play;
		page.run(artifactMediaGateScript(false));
		expect((page.window.HTMLMediaElement as { prototype: { play: unknown } }).prototype.play).toBe(play);
	});
});
