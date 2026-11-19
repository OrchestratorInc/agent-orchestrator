import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");

// The Files screens are route modules, so these read their source for the
// behaviour each one keeps.
describe("the Files list", () => {
	const index = source("../../app/files/[sessionId]/index.tsx");

	// A folder is a page of its own: the edge swipe, Android's back and the back
	// button's history all climb one level instead of leaving Files.
	it("opens each folder as a page titled with its name", () => {
		expect(source("./useFileActions.ts")).toContain('router.push({ pathname: "/files/[sessionId]/folder", params: { sessionId, dir, hostId, ...picked } })');
		expect(source("../../app/files/[sessionId]/folder.tsx")).toContain("navigation.setOptions({ title: name })");
	});

	// A bar in the header on All files only moved the tabs under it on every
	// switch. Its results are All files', so typing on Changes moves there, and
	// going back to Changes clears the search.
	it("keeps the search on both tabs, its results on All files", () => {
		expect(index).toContain("<FileSearchBar search={search} />");
		expect(index).not.toContain('tab === "all" ? <FileSearchBar');
		expect(index).toContain('if (searchActive) setTab("all");');
		expect(index).toContain('if (next !== "all") dismissSearch();');
		expect(source("./FileSearch.tsx")).toContain("bar.current?.cancelSearch();");
	});
});

describe("the Files list's loading state", () => {
	// A worktree pick has no groups until a manifest arrives, so a failed first
	// read must end the spinner and show its error with Try again.
	// One degraded poll must not drop the pickers and the pick, and a branch list
	// that failed to load comes back with a pull to refresh.
	it("settles the pickers once per machine and retries a failed branch list", () => {
		const index = source("../../app/files/[sessionId]/index.tsx");
		expect(index).toContain("const capable = useGitCapable(");
		expect(index).not.toContain("scopesAvailable(manifest)");
		expect(index).toContain("reloadBranches();");
	});

	it("shows a failed manifest read instead of loading forever", () => {
		expect(source("../../app/files/[sessionId]/index.tsx")).toContain("!(error && fromManifest)");
	});
});

describe("the file viewer", () => {
	const file = source("../../app/files/[sessionId]/file.tsx");

	// The daemon's images need the Bearer header, which React Native's Android
	// loader forwards only for an array source.
	it("loads authenticated image diffs on Android", () => {
		expect(file).toContain('source={Platform.OS === "android" ? [pane.source] : pane.source}');
	});

	// At a large text size the drag strip has to cover the scaled +/− column too.
	it("sizes the drag-to-select strip to the scaled gutter and marker", () => {
		expect(file).toContain("gutterWidth: gutterWidth(digits, codeScale) + MARKER_WIDTH * codeScale,");
	});

	// The composer rides the keyboard by a transform, which no layout reports, so
	// the toast over it counts the keyboard's lift as well as the composer's height.
	it("keeps a toast above the composer while the keyboard is up", () => {
		const components = source("./components.tsx");
		expect(components).toContain("useKeyboardState((state) => state.isVisible ? state.height : 0)");
		expect(components).toContain("onRise?.(height + lift)");
		expect(file).toContain("onRise={setComposerRise}");
	});
});
