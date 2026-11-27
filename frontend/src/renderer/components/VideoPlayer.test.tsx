import { act, fireEvent, render, screen } from "@testing-library/react";
import { Profiler } from "react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MainViewVisibleContext } from "../lib/main-view-visibility";
import { formatMediaTime, VideoPlayer } from "./VideoPlayer";

function renderPlayer(props: Partial<Parameters<typeof VideoPlayer>[0]> = {}) {
	render(<VideoPlayer label="clips/demo.mp4" src="http://127.0.0.1:3001/demo.mp4" {...props} />);
	return screen.getByLabelText("clips/demo.mp4") as HTMLVideoElement;
}

/** The events a browser sends for a double-click, at any speed the system recognizes. */
function doubleClick(video: HTMLVideoElement) {
	fireEvent.click(video, { detail: 1 });
	fireEvent.click(video, { detail: 2 });
	fireEvent.doubleClick(video, { detail: 2 });
}

/** Lets the player offer fullscreen; afterEach removes it again. */
function stubFullscreen() {
	Object.defineProperty(document, "fullscreenEnabled", { configurable: true, value: true });
	const requestFullscreen = vi.fn(() => Promise.resolve());
	HTMLElement.prototype.requestFullscreen = requestFullscreen;
	return requestFullscreen;
}

/** jsdom has no media pipeline: give the element a length and announce it. */
function loadMetadata(video: HTMLVideoElement, duration: number) {
	Object.defineProperty(video, "duration", { configurable: true, value: duration });
	fireEvent.durationChange(video);
}

describe("VideoPlayer", () => {
	let play: ReturnType<typeof vi.spyOn>;
	let pause: ReturnType<typeof vi.spyOn>;
	beforeEach(() => {
		play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (this: HTMLMediaElement) {
			this.dispatchEvent(new Event("play"));
			return Promise.resolve();
		});
		pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(function (this: HTMLMediaElement) {
			this.dispatchEvent(new Event("pause"));
		});
	});
	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
		delete (document as { fullscreenEnabled?: boolean }).fullscreenEnabled;
		delete (HTMLElement.prototype as { requestFullscreen?: unknown }).requestFullscreen;
	});

	it("replaces the native controls with its own", () => {
		const video = renderPlayer({ crossOrigin: "anonymous" });
		expect(video).not.toHaveAttribute("controls");
		expect(video).toHaveAttribute("autoplay");
		expect(video).toHaveAttribute("src", "http://127.0.0.1:3001/demo.mp4");
		expect(video).toHaveAttribute("crossorigin", "anonymous");
		for (const name of ["Play", "Mute", "Loop", "Playback speed"]) {
			expect(screen.getByRole("button", { name })).toBeInTheDocument();
		}
		for (const name of ["Back 5 seconds", "Forward 5 seconds"]) {
			expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
		}
		expect(screen.getByRole("slider", { name: "Seek" })).toBeDisabled();
		expect(screen.getByText("0:00 / 0:00")).toBeInTheDocument();
	});

	it("moves the scrubber every frame without re-rendering the controls", () => {
		const frames: FrameRequestCallback[] = [];
		vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => frames.push(callback));
		vi.stubGlobal("cancelAnimationFrame", () => {});
		try {
			let commits = 0;
			render(
				<Profiler id="player" onRender={() => { commits++; }}>
					<VideoPlayer label="clips/demo.mp4" src="http://127.0.0.1:3001/demo.mp4" />
				</Profiler>,
			);
			const video = screen.getByLabelText("clips/demo.mp4") as HTMLVideoElement;
			const track = screen.getByRole("slider", { name: "Seek" }).previousElementSibling as HTMLElement;
			loadMetadata(video, 10);
			fireEvent.play(video);
			const rendered = commits;
			for (const time of [1, 2.5, 5]) {
				video.currentTime = time;
				act(() => frames.shift()?.(0));
			}
			// The fill follows the playhead, but nothing re-rendered.
			expect(track.style.getPropertyValue("--slider-value")).toBe("50%");
			expect(commits).toBe(rendered);
			// The time label still follows the media's own progress events.
			fireEvent.timeUpdate(video);
			expect(screen.getByText("0:05 / 0:10")).toBeInTheDocument();
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it("plays and pauses from the button", async () => {
		const user = userEvent.setup();
		const video = renderPlayer();
		await user.click(screen.getByRole("button", { name: "Play" }));
		expect(play).toHaveBeenCalledTimes(1);
		Object.defineProperty(video, "paused", { configurable: true, value: false });
		await user.click(screen.getByRole("button", { name: "Pause" }));
		expect(pause).toHaveBeenCalledTimes(1);
	});

	it("toggles playback at once when the video is clicked", () => {
		const video = renderPlayer();
		fireEvent.click(video, { detail: 1 });
		expect(play).toHaveBeenCalledTimes(1);
		// A keyboard or assistive click reports no click count.
		Object.defineProperty(video, "paused", { configurable: true, value: false });
		fireEvent.click(video, { detail: 0 });
		expect(pause).toHaveBeenCalledTimes(1);
	});

	it("toggles a playing clip once on a double-click instead of stopping and restarting it", () => {
		const requestFullscreen = stubFullscreen();
		const video = renderPlayer();
		Object.defineProperty(video, "paused", { configurable: true, value: false });
		const pauseEvents = vi.fn();
		video.addEventListener("pause", pauseEvents);
		doubleClick(video);
		expect(pauseEvents).toHaveBeenCalledTimes(1);
		expect(play).not.toHaveBeenCalled();
		// Fullscreen is on its button and the F key, not the double-click.
		expect(requestFullscreen).not.toHaveBeenCalled();
	});

	it("goes fullscreen from its button and the F key", () => {
		const requestFullscreen = stubFullscreen();
		const video = renderPlayer();
		fireEvent.click(screen.getByRole("button", { name: "Full screen" }));
		expect(requestFullscreen).toHaveBeenCalledTimes(1);
		fireEvent.keyDown(video, { key: "f" });
		expect(requestFullscreen).toHaveBeenCalledTimes(2);
		expect(play).not.toHaveBeenCalled();
	});

	it("leaves keys typed in the fullscreen speed menu to the menu", async () => {
		const user = userEvent.setup();
		Object.defineProperty(document, "fullscreenEnabled", { configurable: true, value: true });
		const exitFullscreen = vi.fn(() => Promise.resolve());
		document.exitFullscreen = exitFullscreen;
		try {
			const video = renderPlayer();
			loadMetadata(video, 30);
			const root = video.closest("[data-video-player]")!;
			Object.defineProperty(document, "fullscreenElement", { configurable: true, get: () => root });
			act(() => {
				document.dispatchEvent(new Event("fullscreenchange"));
			});
			await user.click(screen.getByRole("button", { name: "Playback speed" }));
			const item = screen.getByRole("menuitemradio", { name: "1.5×" });
			// In fullscreen the menu portals into the player itself.
			expect(root.contains(item)).toBe(true);
			fireEvent.keyDown(item, { key: "ArrowRight" });
			fireEvent.keyDown(item, { key: "f" });
			expect(video.currentTime).toBe(0);
			expect(exitFullscreen).not.toHaveBeenCalled();
		} finally {
			delete (document as { fullscreenEnabled?: boolean }).fullscreenEnabled;
			delete (document as { fullscreenElement?: unknown }).fullscreenElement;
			delete (document as { exitFullscreen?: unknown }).exitFullscreen;
		}
	});

	it("seeks from the scrubber and the arrow keys", () => {
		const video = renderPlayer();
		loadMetadata(video, 30);
		const scrubber = screen.getByRole("slider", { name: "Seek" });
		expect(scrubber).toBeEnabled();
		fireEvent.change(scrubber, { target: { value: "12" } });
		expect(video.currentTime).toBe(12);
		expect(screen.getByText("0:12 / 0:30")).toBeInTheDocument();
		fireEvent.keyDown(video, { key: "ArrowLeft" });
		expect(video.currentTime).toBe(7);
		fireEvent.keyDown(video, { key: "ArrowRight" });
		fireEvent.keyDown(video, { key: "ArrowRight" });
		expect(video.currentTime).toBe(17);
		// Skipping stops at either end.
		for (let press = 0; press < 3; press++) fireEvent.keyDown(video, { key: "ArrowRight" });
		expect(video.currentTime).toBe(30);
	});

	it("toggles play with the space bar on the video", () => {
		const video = renderPlayer();
		fireEvent.keyDown(video, { key: " " });
		expect(play).toHaveBeenCalledTimes(1);
	});

	it("turns looping on and off", async () => {
		const user = userEvent.setup();
		const video = renderPlayer();
		const loop = screen.getByRole("button", { name: "Loop" });
		expect(loop).toHaveAttribute("aria-pressed", "false");
		expect(video.loop).toBe(false);
		await user.click(loop);
		expect(loop).toHaveAttribute("aria-pressed", "true");
		expect(video.loop).toBe(true);
		await user.click(loop);
		expect(video.loop).toBe(false);
	});

	it("changes the playback speed from its menu", async () => {
		const user = userEvent.setup();
		const video = renderPlayer();
		const trigger = screen.getByRole("button", { name: "Playback speed" });
		expect(trigger).toHaveTextContent("1×");
		await user.click(trigger);
		expect(screen.getByRole("menuitemradio", { name: "1×" })).toHaveAttribute("aria-checked", "true");
		await user.click(screen.getByRole("menuitemradio", { name: "1.5×" }));
		expect(video.playbackRate).toBe(1.5);
		expect(video.defaultPlaybackRate).toBe(1.5);
		expect(trigger).toHaveTextContent("1.5×");
	});

	it("mutes, unmutes, and sets the volume", async () => {
		const user = userEvent.setup();
		const video = renderPlayer();
		// Videos start at half volume.
		expect(video.volume).toBe(0.5);
		expect(screen.getByRole("slider", { name: "Volume" })).toHaveAttribute("aria-valuetext", "50%");
		await user.click(screen.getByRole("button", { name: "Mute" }));
		expect(video.muted).toBe(true);
		fireEvent.volumeChange(video);
		await user.click(screen.getByRole("button", { name: "Unmute" }));
		expect(video.muted).toBe(false);
		fireEvent.volumeChange(video);
		fireEvent.change(screen.getByRole("slider", { name: "Volume" }), { target: { value: "0.4" } });
		expect(video.volume).toBeCloseTo(0.4);
		fireEvent.change(screen.getByRole("slider", { name: "Volume" }), { target: { value: "0" } });
		expect(video.muted).toBe(true);
		// Unmuting from zero comes back at the default level, not full volume.
		await user.click(screen.getByRole("button", { name: "Unmute" }));
		expect(video.volume).toBe(0.5);
		expect(video.muted).toBe(false);
	});

	it("starts a new file version from the beginning, keeping loop and speed", async () => {
		const user = userEvent.setup();
		const view = render(<VideoPlayer label="demo.mp4" src="http://127.0.0.1:3001/demo.mp4?v=1" />);
		const first = screen.getByLabelText("demo.mp4") as HTMLVideoElement;
		loadMetadata(first, 30);
		fireEvent.change(screen.getByRole("slider", { name: "Seek" }), { target: { value: "12" } });
		await user.click(screen.getByRole("button", { name: "Loop" }));
		await user.click(screen.getByRole("button", { name: "Playback speed" }));
		await user.click(screen.getByRole("menuitemradio", { name: "2×" }));

		view.rerender(<VideoPlayer label="demo.mp4" src="http://127.0.0.1:3001/demo.mp4?v=2" />);
		const next = screen.getByLabelText("demo.mp4") as HTMLVideoElement;
		expect(next).not.toBe(first);
		expect(next.currentTime).toBe(0);
		expect(screen.getByText("0:00 / 0:00")).toBeInTheDocument();
		expect(next.loop).toBe(true);
		expect(next.playbackRate).toBe(2);
	});

	it("autoplays a new file version that replaces a playing one", () => {
		const view = render(<VideoPlayer label="demo.mp4" src="http://127.0.0.1:3001/demo.mp4?v=1" />);
		fireEvent.play(screen.getByLabelText("demo.mp4"));
		view.rerender(<VideoPlayer label="demo.mp4" src="http://127.0.0.1:3001/demo.mp4?v=2" />);
		expect(screen.getByLabelText("demo.mp4")).toHaveAttribute("autoplay");
	});

	it("keeps a new file version paused when the person paused the one it replaces", () => {
		const view = render(<VideoPlayer label="demo.mp4" src="http://127.0.0.1:3001/demo.mp4?v=1" />);
		const first = screen.getByLabelText("demo.mp4");
		fireEvent.play(first);
		fireEvent.pause(first);
		view.rerender(<VideoPlayer label="demo.mp4" src="http://127.0.0.1:3001/demo.mp4?v=2" />);
		expect(screen.getByLabelText("demo.mp4")).not.toHaveAttribute("autoplay");
		expect(play).not.toHaveBeenCalled();
	});

	it("autoplays a new version on show when only hiding the pane paused the old one", () => {
		const player = (active: boolean, version: number) => <VideoPlayer active={active} label="demo.mp4" src={`http://127.0.0.1:3001/demo.mp4?v=${version}`} />;
		const view = render(player(true, 1));
		const first = screen.getByLabelText("demo.mp4") as HTMLVideoElement;
		fireEvent.play(first);
		// Hidden while playing: the player pauses it. A rewrite then arrives while it is hidden.
		Object.defineProperty(first, "paused", { configurable: true, value: false });
		view.rerender(player(false, 1));
		expect(pause).toHaveBeenCalledTimes(1);
		view.rerender(player(false, 2));
		// Shown again: the person never stopped it, so the new version starts.
		view.rerender(player(true, 2));
		expect(play).toHaveBeenCalledTimes(1);
		expect(screen.getByLabelText("demo.mp4")).toHaveAttribute("autoplay");
	});

	it("keeps a new version paused on show when the person paused the old one before it was hidden", () => {
		const player = (active: boolean, version: number) => <VideoPlayer active={active} label="demo.mp4" src={`http://127.0.0.1:3001/demo.mp4?v=${version}`} />;
		const view = render(player(true, 1));
		const first = screen.getByLabelText("demo.mp4");
		fireEvent.play(first);
		fireEvent.pause(first);
		view.rerender(player(false, 1));
		view.rerender(player(false, 2));
		view.rerender(player(true, 2));
		expect(play).not.toHaveBeenCalled();
		expect(screen.getByLabelText("demo.mp4")).not.toHaveAttribute("autoplay");
	});

	it("ignores a pause reported by the element a new file version replaced", () => {
		const view = render(<VideoPlayer label="demo.mp4" src="http://127.0.0.1:3001/demo.mp4?v=1" />);
		const first = screen.getByLabelText("demo.mp4");
		fireEvent.play(first);
		view.rerender(<VideoPlayer label="demo.mp4" src="http://127.0.0.1:3001/demo.mp4?v=2" />);
		fireEvent.pause(first);
		expect(screen.getByLabelText("demo.mp4")).toHaveAttribute("autoplay");
	});

	it("plays only while its pane is visible", () => {
		const view = render(<VideoPlayer active={false} label="clips/demo.mp4" src="http://127.0.0.1:3001/demo.mp4" />);
		const video = screen.getByLabelText("clips/demo.mp4") as HTMLVideoElement;
		// A hidden pane, such as an inactive file tab, does not autoplay.
		expect(video).not.toHaveAttribute("autoplay");
		expect(play).not.toHaveBeenCalled();
		// Shown for the first time: it starts.
		view.rerender(<VideoPlayer active label="clips/demo.mp4" src="http://127.0.0.1:3001/demo.mp4" />);
		expect(play).toHaveBeenCalledTimes(1);
		// Hidden again while playing: it pauses, so two tabs never play at once.
		Object.defineProperty(video, "paused", { configurable: true, value: false });
		view.rerender(<VideoPlayer active={false} label="clips/demo.mp4" src="http://127.0.0.1:3001/demo.mp4" />);
		expect(pause).toHaveBeenCalledTimes(1);
		// Shown again after it has played: it stays paused for the person to resume.
		Object.defineProperty(video, "paused", { configurable: true, value: true });
		Object.defineProperty(video, "played", { configurable: true, value: { length: 1 } });
		view.rerender(<VideoPlayer active label="clips/demo.mp4" src="http://127.0.0.1:3001/demo.mp4" />);
		expect(play).toHaveBeenCalledTimes(1);
	});

	it("pauses while global Settings covers the main view", () => {
		const player = (mainViewVisible: boolean) => (
			<MainViewVisibleContext.Provider value={mainViewVisible}>
				<VideoPlayer label="clips/demo.mp4" src="http://127.0.0.1:3001/demo.mp4" />
			</MainViewVisibleContext.Provider>
		);
		const view = render(player(true));
		const video = screen.getByLabelText("clips/demo.mp4") as HTMLVideoElement;
		expect(video).toHaveAttribute("autoplay");
		// Settings opens while it plays: it pauses even though its own pane is active.
		Object.defineProperty(video, "paused", { configurable: true, value: false });
		view.rerender(player(false));
		expect(pause).toHaveBeenCalledTimes(1);
		expect(video).not.toHaveAttribute("autoplay");
		// Settings closes: it stays paused where it was for the person to resume.
		Object.defineProperty(video, "paused", { configurable: true, value: true });
		Object.defineProperty(video, "played", { configurable: true, value: { length: 1 } });
		view.rerender(player(true));
		expect(play).not.toHaveBeenCalled();
	});

	it("waits for the main view before autoplaying a video that opened behind Settings", () => {
		const player = (mainViewVisible: boolean) => (
			<MainViewVisibleContext.Provider value={mainViewVisible}>
				<VideoPlayer label="clips/demo.mp4" src="http://127.0.0.1:3001/demo.mp4" />
			</MainViewVisibleContext.Provider>
		);
		const view = render(player(false));
		const video = screen.getByLabelText("clips/demo.mp4") as HTMLVideoElement;
		expect(video).not.toHaveAttribute("autoplay");
		view.rerender(player(true));
		expect(play).toHaveBeenCalledTimes(1);
	});

	it("reports a failed load", () => {
		const onError = vi.fn();
		const video = renderPlayer({ onError });
		fireEvent.error(video);
		expect(onError).toHaveBeenCalledTimes(1);
	});

	it("formats times as m:ss and h:mm:ss", () => {
		expect(formatMediaTime(0)).toBe("0:00");
		expect(formatMediaTime(9.7)).toBe("0:09");
		expect(formatMediaTime(75)).toBe("1:15");
		expect(formatMediaTime(3725)).toBe("1:02:05");
		expect(formatMediaTime(Number.NaN)).toBe("0:00");
		expect(formatMediaTime(Number.POSITIVE_INFINITY)).toBe("0:00");
	});
});
