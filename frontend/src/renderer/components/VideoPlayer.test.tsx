import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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
		for (const name of ["Play", "Back 5 seconds", "Forward 5 seconds", "Mute", "Loop", "Playback speed"]) {
			expect(screen.getByRole("button", { name })).toBeInTheDocument();
		}
		expect(screen.getByRole("slider", { name: "Seek" })).toBeDisabled();
		expect(screen.getByText("0:00 / 0:00")).toBeInTheDocument();
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
		// A click on the video waits briefly in case it starts a double-click.
		await waitFor(() => expect(play).toHaveBeenCalledTimes(2));
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
			await new Promise((resolve) => setTimeout(resolve, 400));
			expect(requestFullscreen).toHaveBeenCalledTimes(1);
			expect(play).not.toHaveBeenCalled();
			expect(pause).not.toHaveBeenCalled();
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

	it("seeks from the scrubber, the skip buttons, and the arrow keys", async () => {
		const user = userEvent.setup();
		const video = renderPlayer();
		loadMetadata(video, 30);
		const scrubber = screen.getByRole("slider", { name: "Seek" });
		expect(scrubber).toBeEnabled();
		fireEvent.change(scrubber, { target: { value: "12" } });
		expect(video.currentTime).toBe(12);
		expect(screen.getByText("0:12 / 0:30")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Forward 5 seconds" }));
		expect(video.currentTime).toBe(17);
		await user.click(screen.getByRole("button", { name: "Back 5 seconds" }));
		expect(video.currentTime).toBe(12);
		fireEvent.keyDown(video, { key: "ArrowLeft" });
		expect(video.currentTime).toBe(7);
		fireEvent.keyDown(video, { key: "ArrowRight" });
		fireEvent.keyDown(video, { key: "ArrowRight" });
		expect(video.currentTime).toBe(17);
		// Skipping stops at either end.
		for (let press = 0; press < 3; press++) await user.click(screen.getByRole("button", { name: "Forward 5 seconds" }));
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
