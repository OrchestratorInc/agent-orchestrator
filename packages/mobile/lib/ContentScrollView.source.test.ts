import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const read = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");
const native = read("../modules/content-scroll-view/ios/ContentScrollViewModule.swift");
const timeline = read("./chat/ChatTimeline.tsx");

// iOS 26 blurs content under the clear glass bar only on the scroll view the
// bar observes, which UIKit picks when the page appears. The conversation's
// list mounts once it has loaded, after that, and was blurred under the status
// bar alone, with the title over sharp text.
describe("the conversation list under iOS 26's glass bar", () => {
	it("is named as the scroll view the screen's bar observes at the top edge", () => {
		expect(native).toContain("controller.setContentScrollView(scrollView, for: .top)");
		expect(timeline).toContain("<ContentScrollView enabled={underHeader > 0} style={styles.listFrame}>");
	});

	it("withdraws only its own registration", () => {
		expect(native).toContain("controller.contentScrollView(for: .top) === scrollView {\n      controller.setContentScrollView(nil, for: .top)");
	});

	// A page pushed over the chat takes it out of the window; the bar observes
	// the list again the moment it is back.
	it("keeps the registration while another page covers the chat, and drops it once unmounted", () => {
		expect(native).toContain("guard window != nil else { return }");
		expect(native).toContain("if superview == nil {\n      release()");
	});
});
