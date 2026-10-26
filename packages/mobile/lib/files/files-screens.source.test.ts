import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");

// The Files screens are route modules, so these read their source for the
// native contract each one keeps.
describe("the Files list", () => {
	const index = source("../../app/files/[sessionId]/index.tsx");

	// A folder is a page of its own: the edge swipe, Android's back and the back
	// button's history all climb one level instead of leaving Files.
	it("opens each folder as a page titled with its name", () => {
		const actions = source("./useFileActions.ts");
		expect(actions).toContain('router.push({ pathname: "/files/[sessionId]/folder", params: { sessionId, dir, hostId } })');
		const folder = source("../../app/files/[sessionId]/folder.tsx");
		expect(folder).toContain("navigation.setOptions({ title: name })");
		expect(source("../../app/_layout.tsx")).toContain('<Stack.Screen name="files/[sessionId]/folder" options={{ title: "Folder" }} />');
		expect(index).not.toContain("setDir");
		expect(index).not.toContain("crumb");
	});

	it("searches from the header, and only while All files is showing", () => {
		expect(index).toMatch(/\{tab === "all" \? \(\s*<Stack\.SearchBar/);
		expect(index).not.toContain("<TextInput");
		expect(index).toMatch(/<Stack\.SearchBar[^>]*onCancelButtonPress=\{searchClosed\}[^>]*onClose=\{searchClosed\}/);
	});

	it("tints the pull-to-refresh spinner on both platforms", () => {
		expect(index).toContain("{...refreshControlColors(t)}");
		expect(source("./FolderList.tsx")).toContain("{...refreshControlColors(t)}");
	});

	it("chooses its view with the platform's segmented control, the count in the label", () => {
		expect(index).toContain("<SegmentedChoice<Tab>");
		expect(index).toContain('label: summary?.files ? `Changes ${summary.files}` : "Changes"');
	});

	// Opening a row is navigation, which the platforms do not mark with a haptic.
	it("opens rows without a haptic, with each platform's press feedback", () => {
		const rows = source("./rows.tsx");
		expect(rows).not.toContain("haptics");
		expect(rows).toContain('usePressFeedback("row")');
		expect(rows).toContain("rowDividerWidth");
		expect(rows).not.toContain("hairlineWidth");
	});
});

// RN's Modal has no sheet gestures and no detents, and the pill's shadow was a
// hard-coded black; the queue is a form sheet route opened from a native button.
describe("the review queue", () => {
	it("is a form sheet route at half and full height", () => {
		expect(source("../../app/_layout.tsx")).toContain('{ name: "files/[sessionId]/review", detents: [0.5, 1] }');
		expect(source("./useFileActions.ts")).toContain('router.push({ pathname: "/files/[sessionId]/review", params: { sessionId, hostId } })');
		const components = source("./components.tsx");
		expect(components).not.toContain("<Modal");
		expect(components).not.toContain("#000");
	});

	it("floats as the platform's own prominent button on every Files page", () => {
		for (const page of ["index", "folder", "file"]) {
			expect(source(`../../app/files/[sessionId]/${page}.tsx`)).toMatch(/<ReviewButton count=\{review\.comments\.length\}[^>]*onPress=\{openReview\} \/>/);
		}
	});
});

describe("the file viewer", () => {
	const file = source("../../app/files/[sessionId]/file.tsx");

	// iOS steps from the header's chevrons, as Mail does: UIKit's bottom toolbar
	// is driven only from iOS 18 and stays on the navigation controller once
	// shown. Android floats Material's toolbar over this page alone.
	it("steps through changed files with native controls", () => {
		expect(file).toContain('const STEP_IN_HEADER = Platform.OS === "ios";');
		expect(file).toMatch(/STEP_IN_HEADER && canStep \? \(\s*<Stack\.Toolbar\.Button icon=\{fileIcon\("previous"\)\}/);
		expect(file).toMatch(/!STEP_IN_HEADER && canStep && !selection \? \(\s*<Stack\.Toolbar placement="bottom">/);
		expect(file).toContain("{stepLabel}</Text>");
		expect(file).not.toContain("TOOLBAR_HEIGHT");
	});

	// iOS shows "3 of 12" beside the folder; a long folder gives way, not the place.
	it("keeps the file's place readable beside a long folder path", () => {
		expect(file).toMatch(/style=\{styles\.metaPlace\}>\{dir \? `\$\{place\} · ` : place\}<\/Text>/);
		expect(file).toContain("metaPlace: { flexShrink: 0,");
		expect(file).toMatch(/ellipsizeMode="head" style=\{styles\.metaText\}>\{`\$\{dir\}\/`\}<\/Text>/);
	});

	it("drops the tap-to-copy path bar for the header menu's Copy path", () => {
		expect(file).not.toContain("pathPress");
		expect(file).not.toContain("Copy path`");
		expect(file).toContain("<SegmentedChoice<ViewChoice>");
	});

	// The daemon's images need the Bearer header, which React Native's Android
	// loader forwards only for an array source.
	it("loads authenticated image diffs on Android and opens them to zoom", () => {
		expect(file).toContain('source={Platform.OS === "android" ? [pane.source] : pane.source}');
		expect(file).toContain("<ImageViewer visible");
		expect(file).not.toMatch(/textTransform: "uppercase"/);
	});

	it("confirms with the shared toast", () => {
		expect(file).toMatch(/<ToastHost\s+toast=\{toast\}/);
		expect(file).not.toContain("setNotice");
		expect(file).not.toContain("announceForAccessibility");
	});
});
