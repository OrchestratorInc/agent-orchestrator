import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");

// A page's header actions are native header items: bar-button items on iOS,
// Compose icon buttons on Android, each with the platform's press feedback,
// target and tint. Declared with Stack.Toolbar from the page itself, they also
// leave the header with the page, error boundary included.
describe("native header items", () => {
	it("puts the review page's actions in the header toolbar", () => {
		const review = source("../app/review/[sessionId].tsx");
		const toolbar = review.slice(review.indexOf('<Stack.Toolbar placement="right">'), review.indexOf("</Stack.Toolbar>"));
		expect(toolbar).toContain('icon={toolbarIcon("openExternal")}');
		expect(toolbar).toContain("accessibilityLabel={`Open pull request ${reviewPRNumber} in GitHub`}");
		expect(toolbar).toContain('icon={toolbarIcon("more")} accessibilityLabel="More review actions"');
		expect(review).toContain("{headerItems}");
		expect(toolbar).toContain('pathname: "/sheets/review-actions"');
		expect(review).not.toContain("headerRight");
	});

	it("makes the file viewer's wrap control a selectable header item", () => {
		const file = source("../app/files/[sessionId]/file.tsx");
		const toolbar = file.slice(file.indexOf('<Stack.Toolbar placement="right">'), file.indexOf("</Stack.Toolbar>"));
		expect(toolbar).toContain('icon={toolbarIcon("wrapLines")}');
		expect(toolbar).toContain("selected={wrap}");
		expect(toolbar).toContain("setWrap((value) => !value)");
		expect(file).not.toContain("headerRight");
		expect(file).not.toContain("headerAction");
	});

	it("draws the shared header button's unread dot natively", () => {
		const ui = source("./ui.tsx");
		const button = ui.slice(ui.indexOf("export function HeaderIconButton"), ui.indexOf("export const MASCOT_ASPECT"));
		expect(button).toContain("badge={badge}");
		expect(button).not.toContain("headerBadge");
	});
});
