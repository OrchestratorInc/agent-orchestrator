import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { installScrollLockBouncePassThrough, NATIVE_BOUNCE_Y } from "./lib/native-overscroll";

/**
 * The shell runs with Electron's `scrollBounce` (main/window-composition.ts).
 * On macOS it is window-wide: Chromium gives every scroller whose
 * `overscroll-behavior` is `contain` a native rubber band at its edges. Lists
 * and menus opt in through NATIVE_BOUNCE_Y; every other scroller that must not
 * chain uses `none`, which without the bounce behaves exactly like `contain`.
 *
 * jsdom applies no stylesheet, so the contract is pinned at the source level.
 */

const REPO = resolve(process.cwd(), "..");
const SCANNED_DIRS = ["frontend/src/renderer", "packages/product-ui/src"];

/** The only sources that may say `contain`. Opt a list in with NATIVE_BOUNCE_Y instead. */
const CONTAIN_SOURCES = [
	"frontend/src/renderer/lib/native-overscroll.ts",
	// The settings menu surface utility.
	"frontend/src/renderer/styles.css",
	// The board lane bodies; product-ui cannot import the renderer constant.
	"packages/product-ui/src/SessionsBoardView.tsx",
];

/** Lists and menus that stretch at their ends. Add a surface here when it opts in. */
const BOUNCING_FILES = [
	// Shared surfaces: every dropdown, sub-menu and context menu; selects; command lists.
	"frontend/src/renderer/components/ui/menu-styles.ts",
	"frontend/src/renderer/components/ui/select.tsx",
	"frontend/src/renderer/components/ui/command.tsx",
	// Lists and menus with their own scroller.
	"frontend/src/renderer/components/BrowserPanel.tsx",
	"frontend/src/renderer/components/NotificationCenter.tsx",
	"frontend/src/renderer/components/ReviewerPicker.tsx",
	"frontend/src/renderer/components/SearchablePicker.tsx",
	"frontend/src/renderer/components/Sidebar.tsx",
	"frontend/src/renderer/components/chat/AgentSwitchPicker.tsx",
	"frontend/src/renderer/components/chat/ChatAnnotationSummary.tsx",
	"frontend/src/renderer/components/chat/ComposerSuggestMenu.tsx",
	"frontend/src/renderer/components/chat/ModelMenuChoices.tsx",
	"frontend/src/renderer/components/settings/AgentModelCombobox.tsx",
	"frontend/src/renderer/components/settings/SettingsOptionMenu.tsx",
	"frontend/src/renderer/components/settings/harness/CatalogProviderPicker.tsx",
	"frontend/src/renderer/components/teams/PeopleField.tsx",
];

const CONTAIN = /(?<![\w-])overscroll-(?:[xy]-)?contain(?![\w-])|overscroll-behavior(?:-[a-z]+)?\s*:\s*contain/;

function sourceFiles(dir: string): string[] {
	return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
		const path = join(dir, entry.name);
		if (entry.isDirectory()) return entry.name === "node_modules" ? [] : sourceFiles(path);
		if (!/\.(tsx?|css)$/.test(entry.name) || /\.test\.tsx?$/.test(entry.name)) return [];
		return [path];
	});
}

function read(file: string): string {
	return readFileSync(resolve(REPO, file), "utf8");
}

describe("native overscroll (macOS rubber band)", () => {
	it("keeps overscroll-behavior: contain to the bounce sources", () => {
		const containing = SCANNED_DIRS.flatMap((dir) => sourceFiles(resolve(REPO, dir)))
			.filter((path) => CONTAIN.test(readFileSync(path, "utf8")))
			.map((path) => relative(REPO, path))
			.sort();
		expect(containing).toEqual([...CONTAIN_SOURCES].sort());
	});

	it("bounces vertically only, and not under reduced motion", () => {
		expect(NATIVE_BOUNCE_Y.split(" ")).toEqual(["overscroll-y-contain", "overscroll-x-none", "motion-reduce:overscroll-y-none"]);
		const lane = read("packages/product-ui/src/SessionsBoardView.tsx");
		expect(lane).toContain("overscroll-y-contain");
		expect(lane).toContain("motion-reduce:overscroll-y-none");
		const surface = read("frontend/src/renderer/styles.css").match(/@utility settings-menu-surface \{[\s\S]*?\n\}/)?.[0] ?? "";
		expect(surface).toMatch(/overscroll-behavior-y:\s*contain/);
		expect(surface).toMatch(/overscroll-behavior-x:\s*none/);
		expect(surface).toMatch(/@media \(prefers-reduced-motion: reduce\) \{\s*overscroll-behavior-y:\s*none/);
	});

	it.each(BOUNCING_FILES)("%s opts its list in", (file) => {
		expect(read(file)).toMatch(/\$\{NATIVE_BOUNCE_Y\}|NATIVE_BOUNCE_Y[,)]/);
	});

	it("never sets overscroll-behavior: none on the document root", () => {
		// Chromium applies the root's policy to nested scrollers' rubber band, so a
		// root `none` silently turns the bounce off everywhere. The page itself
		// cannot stretch anyway: body is overflow: hidden.
		const rootRules = read("frontend/src/renderer/styles.css")
			.split("}")
			.map((chunk) => chunk.split("{"))
			.filter(([selector]) =>
				selector?.split(",").some((part) => ["html", "body", "#root", ":root"].includes(part.trim())),
			)
			.map(([, body]) => body ?? "");
		expect(rootRules.length).toBeGreaterThan(0);
		for (const body of rootRules) expect(body).not.toMatch(/overscroll-behavior[a-z-]*\s*:\s*none/);
		expect(read("frontend/index.html")).not.toMatch(/overscroll-(?:[xy]-)?none/);
	});
});

describe("installScrollLockBouncePassThrough", () => {
	let cleanup: Array<() => void> = [];
	afterEach(() => {
		for (const undo of cleanup) undo();
		cleanup = [];
		document.body.removeAttribute("data-scroll-locked");
		document.body.replaceChildren();
	});

	// Stands in for react-remove-scroll: added after the pass-through, as a lock
	// mounts after startup, and cancels every wheel it sees.
	function setup({ locked = true, layer = true, bouncing = true, overflows = true } = {}) {
		cleanup.push(installScrollLockBouncePassThrough(document));
		const lock = (event: Event) => event.preventDefault();
		document.addEventListener("wheel", lock, { passive: false });
		cleanup.push(() => document.removeEventListener("wheel", lock));
		if (locked) document.body.setAttribute("data-scroll-locked", "1");
		const host = document.createElement("div");
		if (layer) host.setAttribute("role", "menu");
		const list = document.createElement("div");
		if (bouncing) list.className = NATIVE_BOUNCE_Y;
		// jsdom does no layout.
		Object.defineProperty(list, "scrollHeight", { configurable: true, value: overflows ? 600 : 200 });
		Object.defineProperty(list, "clientHeight", { configurable: true, value: 200 });
		const item = document.createElement("div");
		list.append(item);
		host.append(list);
		document.body.append(host);
		return item;
	}

	function wheel(target: Element, init: WheelEventInit = { deltaY: 40 }) {
		const event = new WheelEvent("wheel", { bubbles: true, cancelable: true, ...init });
		target.dispatchEvent(event);
		return event.defaultPrevented;
	}

	it("lets a vertical wheel over a bouncing list in a locked layer reach the native scroll", () => {
		expect(wheel(setup())).toBe(false);
	});

	it.each([
		["no lock is on", { locked: false }],
		["the list is not in a menu, popover or dialog", { layer: false }],
		["the scroller does not opt in", { bouncing: false }],
		// A list that fits is no scroller to Chromium: the wheel would chain past it.
		["the list fits", { overflows: false }],
	])("leaves the wheel to the lock when %s", (_case, options) => {
		expect(wheel(setup(options))).toBe(true);
	});

	it("leaves pinch-zoom and sideways wheels to the lock", () => {
		const item = setup();
		expect(wheel(item, { deltaY: 40, ctrlKey: true })).toBe(true);
		expect(wheel(item, { deltaX: 40, deltaY: 4 })).toBe(true);
	});
});
