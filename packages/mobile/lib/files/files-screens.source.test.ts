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

	// Opening a row is navigation, which the platforms do not mark with a haptic.
	it("opens rows without a haptic, with each platform's press feedback", () => {
		const rows = source("./rows.tsx");
		expect(rows).not.toContain("haptics");
		expect(rows).toContain('usePressFeedback("row")');
		expect(rows).toContain("rowDividerWidth");
		expect(rows).not.toContain("hairlineWidth");
	});
});
