import { describe, expect, it } from "vitest";
import { isUnplayableShareVideo } from "./cloud-share";

describe("isUnplayableShareVideo", () => {
	it.each(["a.avi", "a.MKV", "clips/a.ogv"])("refuses %s", (name) => expect(isUnplayableShareVideo(name)).toBe(true));
	it.each(["a.mp4", "a.MOV", "a.m4v", "a.webm", "a.png", "a.ogg"])("lets %s through", (name) => expect(isUnplayableShareVideo(name)).toBe(false));
});
