import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { isRenderPath } from "./renderFrameModel";

function source(relativePath: string): string {
	return readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");
}

// The expanded page used to be an RN Modal with a drawn header and a 36pt close
// button, which nothing could swipe away.
describe("render viewer", () => {
	const layout = source("../../app/_layout.tsx");
	const route = source("../../app/render.tsx");
	const frame = source("./RenderFrame.tsx");

	it("is a modal route under the native header", () => {
		expect(layout).toContain('<Stack.Screen name="render" options={{ presentation: "modal", title: "Page" }} />');
		expect(route).toContain("<Stack.Screen options={{ title: pageTitle }} />");
		expect(frame).not.toContain("<Modal");
		expect(frame).not.toContain("useSafeAreaInsets");
	});

	// The frame is one large button over the page: a chip highlight alone gave
	// Android no ripple.
	it("expands with each platform's press feedback", () => {
		expect(frame).toContain('const expandFeedback = usePressFeedback("row", { foreground: true });');
		expect(frame).toContain("android_ripple={expandFeedback.android_ripple}");
		expect(frame).toContain("pressed && expandFeedback.pressed");
	});

	// iOS closes a modal from its bar; Android's top app bar has the system back.
	it("closes from an iOS bar item", () => {
		expect(route).toMatch(/Platform\.OS === "ios" \? \(\s*<Stack\.Toolbar placement="right">\s*<Stack\.Toolbar\.Button icon=\{toolbarIcon\("close"\)\} accessibilityLabel="Close" onPress=\{\(\) => backOr\(router\)\} \/>/);
	});

	it("loads only a session's rendered page, whatever a link names", () => {
		expect(route).toContain("path && isRenderPath(path) ?");
		expect(isRenderPath("/api/v1/sessions/ao-215/renders/render-be45c556")).toBe(true);
		expect(isRenderPath("/api/v1/sessions/ao-215/files")).toBe(false);
		expect(isRenderPath("https://example.com/api/v1/sessions/a/renders/b")).toBe(false);
	});
});
