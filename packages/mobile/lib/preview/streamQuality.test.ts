import { describe, expect, it } from "vitest";
import { adjustStreamQuality, initialStreamQuality, streamLink } from "./streamQuality";

describe("stream quality", () => {
	it("reads the link from expo-network's type", () => {
		expect(streamLink("CELLULAR")).toBe("cellular");
		expect(streamLink("WIFI")).toBe("wifi");
		expect(streamLink("ETHERNET")).toBe("wifi");
		expect(streamLink("UNKNOWN")).toBe("unknown");
		expect(streamLink(undefined)).toBe("unknown");
	});

	it("starts cellular at quality 50 and 15 fps, Wi-Fi as today", () => {
		expect(initialStreamQuality("cellular")).toEqual({ quality: 50, maxFps: 15 });
		expect(initialStreamQuality("wifi")).toEqual({ quality: 70, maxFps: 30 });
	});

	it("steps quality down first, then the frame rate, when the stream is over budget", () => {
		let q = initialStreamQuality("cellular");
		q = adjustStreamQuality(q, 400, "cellular");
		expect(q).toEqual({ quality: 40, maxFps: 15 });
		q = adjustStreamQuality(q, 400, "cellular");
		expect(q).toEqual({ quality: 40, maxFps: 10 });
		for (let i = 0; i < 10; i++) q = adjustStreamQuality(q, 400, "cellular");
		expect(q).toEqual({ quality: 40, maxFps: 5 });
	});

	it("steps the frame rate back up first, then quality, when there is room, up to the link's ceiling", () => {
		let q = { quality: 40, maxFps: 5 };
		q = adjustStreamQuality(q, 10, "wifi");
		expect(q).toEqual({ quality: 40, maxFps: 10 });
		for (let i = 0; i < 20; i++) q = adjustStreamQuality(q, 10, "wifi");
		expect(q).toEqual({ quality: 80, maxFps: 30 });
		for (let i = 0; i < 20; i++) q = adjustStreamQuality(q, 10, "cellular");
		expect(q).toEqual({ quality: 60, maxFps: 20 });
	});

	it("holds steady between half the budget and the budget", () => {
		const q = { quality: 60, maxFps: 20 };
		expect(adjustStreamQuality(q, 100, "cellular")).toBe(q);
	});
});
