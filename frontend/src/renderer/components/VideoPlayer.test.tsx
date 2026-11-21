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
	afterEach(() => vi.restoreAllMocks());

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

	it("plays and pauses from the button and from clicking the video", async () => {
		const user = userEvent.setup();
		const video = renderPlayer();
		await user.click(screen.getByRole("button", { name: "Play" }));
		expect(play).toHaveBeenCalledTimes(1);
		Object.defineProperty(video, "paused", { configurable: true, value: false });
		await user.click(screen.getByRole("button", { name: "Pause" }));
		expect(pause).toHaveBeenCalledTimes(1);
		Object.defineProperty(video, "paused", { configurable: true, value: true });
		await user.click(video);
		expect(play).toHaveBeenCalledTimes(2);
	});

	it("enters fullscreen on a double-click without toggling playback", async () => {
		const user = userEvent.setup();
		Object.defineProperty(document, "fullscreenEnabled", { configurable: true, value: true });
		const requestFullscreen = vi.fn(() => Promise.resolve());
		HTMLElement.prototype.requestFullscreen = requestFullscreen;
		try {
			const video = renderPlayer();
			loadMetadata(video, 2);
			video.currentTime = 1.92;
			await user.dblClick(video);
			expect(requestFullscreen).toHaveBeenCalledTimes(1);
			// The first click started playback; the double-click put it back.
			expect(play).toHaveBeenCalledTimes(1);
			expect(pause).toHaveBeenCalledTimes(1);
			expect(video.currentTime).toBe(1.92);
			expect(screen.getByRole("button", { name: "Play" })).toBeInTheDocument();
		} finally {
			delete (document as { fullscreenEnabled?: boolean }).fullscreenEnabled;
			delete (HTMLElement.prototype as { requestFullscreen?: unknown }).requestFullscreen;
		}
	});

	it("restores playback after a slow double-click the system still recognizes", async () => {
		Object.defineProperty(document, "fullscreenEnabled", { configurable: true, value: true });
		const requestFullscreen = vi.fn(() => Promise.resolve());
		HTMLElement.prototype.requestFullscreen = requestFullscreen;
		try {
			const video = renderPlayer();
			loadMetadata(video, 2);
			video.currentTime = 1.92;
			fireEvent.click(video, { detail: 1 });
			await new Promise((resolve) => setTimeout(resolve, 350));
			video.currentTime = 2;
			fireEvent.click(video, { detail: 2 });
			fireEvent.doubleClick(video, { detail: 2 });
			expect(requestFullscreen).toHaveBeenCalledTimes(1);
			expect(play).toHaveBeenCalledTimes(1);
			expect(pause).toHaveBeenCalledTimes(1);
			expect(video.currentTime).toBe(1.92);
		} finally {
			delete (document as { fullscreenEnabled?: boolean }).fullscreenEnabled;
			delete (HTMLElement.prototype as { requestFullscreen?: unknown }).requestFullscreen;
		}
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
