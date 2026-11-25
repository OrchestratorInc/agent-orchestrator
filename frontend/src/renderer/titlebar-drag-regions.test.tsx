import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { CSSProperties } from "react";
import { render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * On macOS the window has no native title bar: the floating top bar's headers
 * are the title bar. Every empty pixel in that band must be an Electron drag
 * region (which also gives double-click-to-zoom), and only the controls in it
 * may punch through with no-drag. jsdom applies no stylesheet, so the CSS half
 * of the contract is pinned at the source level, like
 * native-composition-cascade.test.ts.
 */

const css = readFileSync(resolve(process.cwd(), "src/renderer/styles.css"), "utf8");
const MAC = 'html[data-ao-platform="darwin"]';

type Rule = { selector: string; body: string };

function rules(): Rule[] {
	return css
		.split("}")
		.map((chunk) => {
			const open = chunk.indexOf("{");
			if (open === -1) return null;
			return { selector: chunk.slice(0, open).trim().replace(/^[\s\S]*\*\//, "").trim(), body: chunk.slice(open + 1).trim() };
		})
		.filter((rule): rule is Rule => rule !== null);
}

function appRegionRules(value: "drag" | "no-drag"): Rule[] {
	return rules().filter((rule) => new RegExp(`-webkit-app-region:\\s*${value}\\s*;`).test(rule.body));
}

// The headers that make up the band, in the order they appear in the DOM.
const BAND_HEADERS = ['[data-testid="session-topbar-host"]', '[data-slot="inspector-container"] .session-inspector__topbar'];

describe("macOS title-bar drag regions", () => {
	it("makes the session tab bar and the inspector's tab row drag the window", () => {
		const drag = appRegionRules("drag").filter((rule) => rule.selector.startsWith(MAC));
		for (const header of BAND_HEADERS) {
			expect(drag.some((rule) => rule.selector.includes(header)), header).toBe(true);
		}
	});

	it("punches only interactive elements out of the band, including the action clusters", () => {
		const punch = appRegionRules("no-drag").find(
			(rule) => rule.selector.startsWith(MAC) && BAND_HEADERS.every((header) => rule.selector.includes(header)),
		);
		expect(punch).toBeDefined();
		expect(punch!.selector).toContain(".workspace-topbar-actions");
		for (const control of ["button", "a", "input", "label", "form", '[role="tab"]', '[role="menuitem"]', "[title]"]) {
			expect(punch!.selector).toMatch(new RegExp(`[(,\\s]${control.replace(/[[\]"=]/g, "\\$&")}[,)]`));
		}
	});

	it("never makes a whole header or action cluster no-drag", () => {
		// A full-height container with no-drag kills drag and double-click in
		// every gap between its controls (the inspector strip bug).
		const containers = [...BAND_HEADERS, ".session-inspector__topbar", ".session-inspector__trailing", ".workspace-topbar-actions", ".panel-topbar-controls", ".session-topbar-actions"];
		for (const rule of appRegionRules("no-drag")) {
			for (const selector of rule.selector.split(/,(?![^(]*\))/).map((part) => part.trim())) {
				for (const container of containers) {
					expect(selector.endsWith(container), `${selector} makes ${container} no-drag`).toBe(false);
				}
			}
		}
	});
});

const mockIsMac = vi.hoisted(() => vi.fn(() => true));

describe("top bar action clusters on macOS", () => {
	beforeEach(() => {
		vi.resetModules();
		mockIsMac.mockReturnValue(true);
		vi.doMock("./lib/platform", async () => ({
			...(await vi.importActual<typeof import("./lib/platform")>("./lib/platform")),
			isMacPlatform: mockIsMac,
		}));
		// The surface hands the bell its no-drag style; the stub keeps it.
		vi.doMock("./components/NotificationCenter", () => ({
			NotificationCenter: ({ style }: { style?: CSSProperties }) => <button aria-label="Notifications" style={style} type="button" />,
		}));
	});

	it("leaves the panel's trailing cluster draggable between its buttons", async () => {
		const { PageTopbar, PageTopbarSurface, PageTopbarTrailingAction, noDragStyle } = await import("./components/PageTopbar");
		render(
			<PageTopbarSurface notifications>
				<PageTopbar actions={<button type="button">New task</button>} />
				<PageTopbarTrailingAction><button style={noDragStyle} type="button">Toggle inspector</button></PageTopbarTrailingAction>
			</PageTopbarSurface>,
		);

		const appRegion = (element: HTMLElement) => (element.style as CSSStyleDeclaration & { WebkitAppRegion?: string }).WebkitAppRegion;
		const header = screen.getByRole("banner");
		expect(appRegion(header)).toBe("drag");
		const controls = screen.getByTestId("panel-topbar-controls");
		expect(appRegion(controls)).toBeFalsy();
		for (const cluster of screen.getAllByTestId("workspace-topbar-actions")) {
			expect(appRegion(cluster)).toBeFalsy();
		}
		expect(within(controls).getAllByRole("button").map(appRegion)).toEqual(["no-drag", "no-drag"]);
	});
});
