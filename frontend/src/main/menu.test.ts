import { describe, expect, it, vi } from "vitest";
import {
	buildLinuxAppMenuTemplate,
	buildMacAppMenuTemplate,
	buildWindowsAppMenuTemplate,
} from "./menu";

type MenuItem = ReturnType<typeof buildWindowsAppMenuTemplate>[number];
type SubmenuItem = NonNullable<Extract<MenuItem["submenu"], readonly unknown[]>>[number];

function viewSubmenu(template: MenuItem[] = buildWindowsAppMenuTemplate()): readonly SubmenuItem[] {
	const viewMenu = template.find((item) => item.label === "View");
	if (!viewMenu || !Array.isArray(viewMenu.submenu)) {
		throw new Error("View menu not found");
	}
	return viewMenu.submenu;
}

describe("buildWindowsAppMenuTemplate", () => {
	it("registers both plus key forms for zoom in", () => {
		const zoomInItems = viewSubmenu().filter((item) => item.role === "zoomIn");

		expect(zoomInItems).toEqual(
			expect.arrayContaining([
				expect.objectContaining({ accelerator: "Ctrl+=", role: "zoomIn" }),
				expect.objectContaining({ accelerator: "Ctrl+Plus", role: "zoomIn", visible: false }),
			]),
		);
	});

	it("keeps the direct minus accelerator for zoom out", () => {
		expect(viewSubmenu()).toContainEqual(expect.objectContaining({ accelerator: "Ctrl+-", role: "zoomOut" }));
	});
});

describe("buildLinuxAppMenuTemplate", () => {
	it("registers zoom and standard accelerators matching Linux expectations", () => {
		const zoomInItems = viewSubmenu(buildLinuxAppMenuTemplate()).filter((item) => item.role === "zoomIn");

		expect(zoomInItems).toEqual(
			expect.arrayContaining([
				expect.objectContaining({ accelerator: "Ctrl+=", role: "zoomIn" }),
				expect.objectContaining({ accelerator: "Ctrl+Plus", role: "zoomIn", visible: false }),
			]),
		);
		expect(viewSubmenu(buildLinuxAppMenuTemplate())).toContainEqual(
			expect.objectContaining({ accelerator: "Ctrl+-", role: "zoomOut" }),
		);
	});

	it("uses a guarded click handler for DevTools when provided", () => {
		const onToggleDevTools = vi.fn();
		const template = buildLinuxAppMenuTemplate(onToggleDevTools, { allowShellDebugging: true });
		const devtoolsItem = viewSubmenu(template).find(
			(item) => item.label === "Toggle DevTools",
		);

		expect(devtoolsItem).toMatchObject({
			accelerator: "Ctrl+Shift+I",
			label: "Toggle DevTools",
		});
		devtoolsItem?.click?.(undefined as never, undefined as never, undefined as never);
		expect(onToggleDevTools).toHaveBeenCalledOnce();
	});
});


describe("buildMacAppMenuTemplate", () => {
	const quit = { appName: "Agent Orchestrator", onQuit: () => undefined };

	function macViewSubmenu(onToggleDevTools = () => undefined): readonly SubmenuItem[] {
		const viewMenu = buildMacAppMenuTemplate(onToggleDevTools, quit, { allowShellDebugging: true }).find(
			(item) => item.label === "View",
		);
		if (!viewMenu || !Array.isArray(viewMenu.submenu)) {
			throw new Error("View menu not found");
		}
		return viewMenu.submenu;
	}

	it("uses a guarded click handler instead of Electron's crashing DevTools role", () => {
		const onToggleDevTools = vi.fn();
		const devtoolsItems = macViewSubmenu(onToggleDevTools).filter(
			(item) => item.label === "Toggle Developer Tools" || item.role === "toggleDevTools",
		);

		expect(devtoolsItems).toHaveLength(1);
		expect(devtoolsItems[0]).toMatchObject({
			accelerator: "Alt+Command+I",
			label: "Toggle Developer Tools",
		});
		expect(devtoolsItems[0].role).toBeUndefined();

		devtoolsItems[0].click?.(undefined as never, undefined as never, undefined as never);
		expect(onToggleDevTools).toHaveBeenCalledOnce();
	});

	it("preserves Electron's complete standard macOS menus", () => {
		const template = buildMacAppMenuTemplate(() => undefined, quit);

		expect(template.map((item) => item.role)).toEqual(
			expect.arrayContaining(["appMenu", "fileMenu", "editMenu", "windowMenu"]),
		);
		expect(macViewSubmenu()).toContainEqual(expect.objectContaining({ role: "forceReload" }));
	});

	it("binds Close Window to Shift+Command+W so Cmd+W cannot kill the application window", () => {
		const template = buildMacAppMenuTemplate(() => undefined, quit);
		const fileMenu = template.find((item) => item.role === "fileMenu");
		expect(fileMenu).toBeDefined();
		const submenu = fileMenu?.submenu;
		expect(Array.isArray(submenu)).toBe(true);
		expect(submenu).toContainEqual(
			expect.objectContaining({
				role: "close",
				accelerator: "Shift+Command+W",
			}),
		);
	});

	it("routes Command+Q through a plain Quit item that reports whether the shortcut fired it", () => {
		const onQuit = vi.fn();
		const appMenu = buildMacAppMenuTemplate(() => undefined, { appName: "Agent Orchestrator", onQuit }).find(
			(item) => item.role === "appMenu",
		);
		const submenu = Array.isArray(appMenu?.submenu) ? appMenu.submenu : [];
		const quitItem = submenu.find((item) => item.accelerator === "Command+Q");

		expect(submenu.some((item) => item.role === "quit")).toBe(false);
		expect(submenu).toEqual(expect.arrayContaining([
			expect.objectContaining({ role: "about" }),
			expect.objectContaining({ role: "hide" }),
		]));
		expect(quitItem).toMatchObject({ label: "Quit Agent Orchestrator" });

		quitItem?.click?.(undefined as never, undefined as never, { triggeredByAccelerator: true } as never);
		quitItem?.click?.(undefined as never, undefined as never, {} as never);
		expect(onQuit.mock.calls).toEqual([[true], [false]]);
	});
});


describe("packaged application menus", () => {
	it.each(["darwin", "win32", "linux"])("never registers shell reload or built-in DevTools roles on %s", (platform) => {
		const toggle = vi.fn();
		const template = platform === "darwin"
			? buildMacAppMenuTemplate(toggle, { appName: "AO", onQuit: vi.fn() })
			: platform === "win32" ? buildWindowsAppMenuTemplate(toggle) : buildLinuxAppMenuTemplate(toggle);
		const submenu = viewSubmenu(template);
		expect(submenu.filter((item) => ["reload", "forceReload", "toggleDevTools"].includes(item.role ?? ""))).toEqual([]);
		expect(submenu.find((item) => item.label === "Toggle Browser Developer Tools")).toBeDefined();
	});

	it.each(["darwin", "win32", "linux"])("routes browser reload menu actions through guarded callbacks on %s", (platform) => {
		const onReload = vi.fn();
		const options = { onReload };
		const toggle = vi.fn();
		const template = platform === "darwin"
			? buildMacAppMenuTemplate(toggle, { appName: "AO", onQuit: vi.fn() }, options)
			: platform === "win32" ? buildWindowsAppMenuTemplate(toggle, options) : buildLinuxAppMenuTemplate(toggle, options);
		const submenu = viewSubmenu(template);
		for (const label of ["Reload Browser", "Force Reload Browser"]) {
			const item = submenu.find((item) => item.label === label);
			expect(item?.role).toBeUndefined();
			item?.click?.(undefined as never, undefined as never, undefined as never);
		}
		expect(onReload.mock.calls).toEqual([[false], [true]]);
	});
});
