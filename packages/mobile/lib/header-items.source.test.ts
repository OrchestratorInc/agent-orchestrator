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

	it("folds the file viewer's controls into one header menu, wrap checked in it", () => {
		const file = source("../app/files/[sessionId]/file.tsx");
		const header = file.slice(file.indexOf("const headerItems = useMemo("), file.indexOf("</Stack.Toolbar>"));
		expect(header).toMatch(/<Stack\.Toolbar placement="right">[\s\S]*<Stack\.Toolbar\.Menu\s+icon=\{fileIcon\("more"\)\}/);
		expect(header).toContain('isOn={wrap} onPress={() => setWrap((value) => !value)}>Wrap lines</Stack.Toolbar.MenuAction>');
		expect(header).toContain("onPress={copyPath}>Copy path</Stack.Toolbar.MenuAction>");
		expect(header).toContain("onPress={sharePath}>Share</Stack.Toolbar.MenuAction>");
		expect(file).not.toContain("headerRight");
		expect(file).not.toContain("headerAction");
	});

	// Every page's title, back button and actions are the native header's, so
	// the drawn header row and its icon button are gone.
	it("leaves no drawn header row to build a page header from", () => {
		const ui = source("./ui.tsx");
		expect(ui).not.toContain("export function ScreenHeader");
		expect(ui).not.toContain("export function HeaderIconButton");
		expect(ui).not.toContain("native-header-button");
	});
});
