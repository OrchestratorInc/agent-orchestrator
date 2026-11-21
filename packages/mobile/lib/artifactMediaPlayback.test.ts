import { describe, expect, it } from "vitest";
import { artifactPagePlaysMedia } from "./artifactMediaPlayback";

describe("artifactPagePlaysMedia", () => {
	it("lets the page on screen play its media", () => {
		expect(artifactPagePlaysMedia(true, '<video src="a.mp4" autoplay muted playsinline></video>')).toBe(true);
		expect(artifactPagePlaysMedia(true, "<AUDIO controls src=a.mp3>")).toBe(true);
		expect(artifactPagePlaysMedia(true, "<script>new Audio('ding.mp3').play()</script>")).toBe(true);
		expect(artifactPagePlaysMedia(true, "<script>const ctx = new webkitAudioContext()</script>")).toBe(true);
	});

	it("keeps a page off screen on the defaults, whatever it holds", () => {
		expect(artifactPagePlaysMedia(false, '<video src="a.mp4" autoplay></video>')).toBe(false);
		expect(artifactPagePlaysMedia(false, undefined)).toBe(false);
	});

	it("keeps a page with no media on the defaults, so it never reloads", () => {
		expect(artifactPagePlaysMedia(true, "<h1>Report</h1><p>Audio levels are fine.</p><img src=chart.png>")).toBe(false);
		expect(artifactPagePlaysMedia(true, "<script>playAudio()</script>")).toBe(false);
	});

	it("lets a page loaded from its address play while on screen", () => {
		expect(artifactPagePlaysMedia(true, undefined)).toBe(true);
	});
});
