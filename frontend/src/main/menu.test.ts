import { describe, expect, it, vi } from "vitest";
import {
	buildLinuxAppMenuTemplate,
	buildMacAppMenuTemplate,
	buildWindowsAppMenuTemplate,
	coerceAppMenuAvailability,
	type AppMenuCommands,
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


describe("macOS top bar commands", () => {
	const quit = { appName: "AO", onQuit: () => undefined };
	const macTemplate = (commands?: AppMenuCommands) => buildMacAppMenuTemplate(() => undefined, quit, { commands });
	const commands = (overrides: Partial<AppMenuCommands> = {}): AppMenuCommands => ({
		accelerator: (id) => (id === "toggle-sidebar" ? "Command+B" : undefined),
		isEnabled: () => false,
		run: vi.fn(),
		...overrides,
	});
	const submenuOf = (template: MenuItem[], match: (item: MenuItem) => boolean): readonly SubmenuItem[] => {
		const menu = template.find(match);
		return Array.isArray(menu?.submenu) ? menu.submenu : [];
	};

	it("adds no command items without commands, so other callers keep today's menu", () => {
		const template = macTemplate();
		expect(template.some((item) => item.label === "Go")).toBe(false);
		expect(viewSubmenu(template).some((item) => item.id === "toggle-sidebar")).toBe(false);
	});

	it("puts commands where a Mac user expects them, before the Window menu", () => {
		const template = macTemplate(commands());
		expect(submenuOf(template, (item) => item.role === "fileMenu").map((item) => item.id).filter(Boolean)).toEqual([
			"new-session",
			"new-shell-terminal",
			"open-in-editor",
			"open-in-file-manager",
			"archive-session",
		]);
		expect(viewSubmenu(template).slice(0, 2).map((item) => item.id)).toEqual(["toggle-sidebar", "toggle-inspector"]);
		expect(submenuOf(template, (item) => item.label === "Go").map((item) => item.id).filter(Boolean)).toEqual([
			"history-back",
			"history-forward",
			"open-orchestrator",
			"toggle-notifications",
		]);
		const labels = template.map((item) => item.label ?? item.role);
		expect(labels.indexOf("Go")).toBe(labels.indexOf("windowMenu") - 1);
	});

	it("shows the binding, and omits the accelerator for a command with none", () => {
		const view = viewSubmenu(macTemplate(commands()));
		expect(view.find((item) => item.id === "toggle-sidebar")).toMatchObject({ accelerator: "Command+B" });
		expect(view.find((item) => item.id === "toggle-inspector")).not.toHaveProperty("accelerator");
	});

	it("reflects availability and reports whether a chord fired the item", () => {
		const run = vi.fn();
		const sidebar = viewSubmenu(macTemplate(commands({ isEnabled: (id) => id === "toggle-sidebar", run }))).find(
			(item) => item.id === "toggle-sidebar",
		);
		expect(sidebar).toMatchObject({ enabled: true });
		sidebar?.click?.(undefined as never, undefined as never, { triggeredByAccelerator: true } as never);
		sidebar?.click?.(undefined as never, undefined as never, {} as never);
		expect(run.mock.calls).toEqual([["toggle-sidebar", true], ["toggle-sidebar", false]]);
	});

	it("keeps Windows and Linux menus free of the commands", () => {
		for (const template of [buildWindowsAppMenuTemplate(), buildLinuxAppMenuTemplate()]) {
			expect(template.some((item) => item.label === "Go" || item.label === "File")).toBe(false);
		}
	});

	it("coerces availability from IPC to known commands and strict booleans", () => {
		const availability = coerceAppMenuAvailability({ "toggle-sidebar": true, "history-back": "yes", bogus: true });
		expect(availability["toggle-sidebar"]).toBe(true);
		expect(availability["history-back"]).toBe(false);
		expect(availability).not.toHaveProperty("bogus");
		expect(coerceAppMenuAvailability(null)["toggle-sidebar"]).toBe(false);
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
