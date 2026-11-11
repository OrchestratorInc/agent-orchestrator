import type { MenuItemConstructorOptions } from "electron";
import { APP_MENU_COMMAND_IDS, type AppMenuAvailability, type AppMenuCommandId } from "../shared/shortcuts";

/** Top bar commands for the macOS menu bar; the renderer owns what each one does. */
export type AppMenuCommands = {
	accelerator: (id: AppMenuCommandId) => string | undefined;
	isEnabled: (id: AppMenuCommandId) => boolean;
	run: (id: AppMenuCommandId, triggeredByAccelerator: boolean) => void;
};

type AppMenuOptions = {
	allowShellDebugging?: boolean;
	onReload?: (ignoreCache: boolean) => void;
	commands?: AppMenuCommands;
};

/** Keeps only known commands, each as a strict boolean. Input comes over IPC. */
export function coerceAppMenuAvailability(raw: unknown): AppMenuAvailability {
	const source = raw && typeof raw === "object" ? (raw as Record<string, unknown>) : {};
	return Object.fromEntries(APP_MENU_COMMAND_IDS.map((id) => [id, source[id] === true]));
}

function commandItem(commands: AppMenuCommands, id: AppMenuCommandId, label: string): MenuItemConstructorOptions {
	const accelerator = commands.accelerator(id);
	return {
		id,
		label,
		enabled: commands.isEnabled(id),
		...(accelerator ? { accelerator } : {}),
		click: (_item, _window, event) => commands.run(id, event.triggeredByAccelerator === true),
	};
}

function macFileCommandItems(commands: AppMenuCommands | undefined): MenuItemConstructorOptions[] {
	if (!commands) return [];
	return [
		commandItem(commands, "new-session", "New Task"),
		commandItem(commands, "new-shell-terminal", "New Terminal"),
		{ type: "separator" },
		commandItem(commands, "open-in-editor", "Open in Editor"),
		commandItem(commands, "open-in-file-manager", "Open in Finder"),
		{ type: "separator" },
		commandItem(commands, "archive-session", "Archive Session"),
		{ type: "separator" },
	];
}

function macViewCommandItems(commands: AppMenuCommands | undefined): MenuItemConstructorOptions[] {
	if (!commands) return [];
	return [
		commandItem(commands, "toggle-sidebar", "Toggle Sidebar"),
		commandItem(commands, "toggle-inspector", "Toggle Inspector"),
		{ type: "separator" },
	];
}

function macGoMenu(commands: AppMenuCommands | undefined): MenuItemConstructorOptions[] {
	if (!commands) return [];
	return [
		{
			label: "Go",
			submenu: [
				commandItem(commands, "history-back", "Back"),
				commandItem(commands, "history-forward", "Forward"),
				{ type: "separator" },
				commandItem(commands, "open-orchestrator", "Orchestrator"),
				commandItem(commands, "toggle-notifications", "Notifications"),
			],
		},
	];
}

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
				...macFileCommandItems(options.commands),
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
				...macViewCommandItems(options.commands),
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
		...macGoMenu(options.commands),
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
