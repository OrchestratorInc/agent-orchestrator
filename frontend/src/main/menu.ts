import type { MenuItemConstructorOptions } from "electron";

type AppMenuOptions = {
	allowShellDebugging?: boolean;
	onReload?: (ignoreCache: boolean) => void;
};

function reloadItems(options: AppMenuOptions, isMac: boolean): MenuItemConstructorOptions[] {
	if (options.allowShellDebugging) return [{ role: "reload" }, { role: "forceReload" }];
	const onReload = options.onReload;
	if (!onReload) return [];
	return [
		{
			label: "Reload Browser",
			accelerator: isMac ? "Command+R" : "Ctrl+R",
			click: () => onReload(false),
		},
		{
			label: "Force Reload Browser",
			accelerator: isMac ? "Shift+Command+R" : "Ctrl+Shift+R",
			click: () => onReload(true),
		},
	];
}

// Electron's built-in toggleDevTools role assumes the focused surface belongs
// to a BrowserWindow. AO uses BaseWindow with WebContentsView children, so the
// role can receive no focused window and crash the main process. Keep Electron's
// complete standard menus through their top-level roles, but replace View so
// DevTools routes through AO's guarded handler. Similarly, Electron's default
// fileMenu role binds Cmd+W to "Close Window", which kills the entire application
// window whenever a tab or terminal close races the native menu. Multi-tab
// macOS applications (Safari, Chrome, VS Code) bind Shift+Command+W to "Close Window"
// so Command+W remains scoped to tabs and terminals. Quit is a plain item, not
// the quit role (which maps straight to NSApp terminate:), so a ⌘Q press can ask
// for confirmation while choosing Quit from the menu still quits at once.
export function buildMacAppMenuTemplate(
	onToggleDevTools: () => void,
	quit: { appName: string; onQuit: (fromShortcut: boolean) => void },
	options: AppMenuOptions = {},
): MenuItemConstructorOptions[] {
	return [
		{
			role: "appMenu",
			submenu: [
				{ role: "about" },
				{ type: "separator" },
				{ role: "services" },
				{ type: "separator" },
				{ role: "hide" },
				{ role: "hideOthers" },
				{ role: "unhide" },
				{ type: "separator" },
				{
					label: `Quit ${quit.appName}`,
					accelerator: "Command+Q",
					click: (_item, _window, event) => quit.onQuit(event.triggeredByAccelerator === true),
				},
			],
		},
		{
			role: "fileMenu",
			submenu: [
				{
					role: "close",
					accelerator: "Shift+Command+W",
				},
			],
		},
		{ role: "editMenu" },
		{
			label: "View",
			submenu: [
				...reloadItems(options, true),
				{
					label: options.allowShellDebugging ? "Toggle Developer Tools" : "Toggle Browser Developer Tools",
					accelerator: "Alt+Command+I",
					click: onToggleDevTools,
				},
				{ type: "separator" },
				{ role: "resetZoom" },
				{ role: "zoomIn" },
				{ role: "zoomOut" },
				{ type: "separator" },
				{ role: "togglefullscreen" },
			],
		},
		{ role: "windowMenu" },
	];
}

export function buildWindowsAppMenuTemplate(onToggleDevTools?: () => void, options: AppMenuOptions = {}): MenuItemConstructorOptions[] {
	const devtoolsItems: MenuItemConstructorOptions[] = onToggleDevTools
		? [{
			label: options.allowShellDebugging ? "Toggle DevTools" : "Toggle Browser Developer Tools",
			accelerator: "Ctrl+Shift+I",
			click: onToggleDevTools,
		}]
		: [];
	return [
		{
			label: "Edit",
			submenu: [
				{ role: "undo" },
				{ role: "redo" },
				{ type: "separator" },
				{ role: "cut" },
				{ role: "copy" },
				{ role: "paste" },
				{ role: "selectAll" },
			],
		},
		{
			label: "View",
			submenu: [
				...reloadItems(options, false),
				...devtoolsItems,
				{ type: "separator" },
				{ role: "resetZoom" },
				{ accelerator: "Ctrl+=", role: "zoomIn" },
				{ accelerator: "Ctrl+Plus", acceleratorWorksWhenHidden: true, role: "zoomIn", visible: false },
				{ accelerator: "Ctrl+-", role: "zoomOut" },
				{ type: "separator" },
				{ role: "togglefullscreen" },
			],
		},
		{
			label: "Window",
			submenu: [{ role: "minimize" }, { role: "close" }],
		},
	];
}

export function buildLinuxAppMenuTemplate(onToggleDevTools?: () => void, options: AppMenuOptions = {}): MenuItemConstructorOptions[] {
	return buildWindowsAppMenuTemplate(onToggleDevTools, options);
}
