import type { BaseWindow, WebContents, WebContentsView } from "electron";

type MainWindowHost = Pick<BaseWindow, "contentView" | "isDestroyed">;

export type WebContentsViewConstructor = new (options: {
	webPreferences: Electron.WebPreferences;
}) => WebContentsView;

export type WindowComposition = {
	shellView: WebContentsView;
	shellWebContents: WebContents;
	setOverlayOpen: (open: boolean) => void;
	restackShell: () => void;
	resize: () => void;
	dispose: () => void;
};

export function createWindowComposition(options: {
	mainWindow: MainWindowHost;
	WebContentsView: WebContentsViewConstructor;
	preload: string;
	platform: NodeJS.Platform;
	allowShellDebugging?: boolean;
	hasLivePageBelow?: () => boolean;
}): WindowComposition {
	const shellView = new options.WebContentsView({
		webPreferences: {
			devTools: options.allowShellDebugging === true,
			preload: options.preload,
			contextIsolation: true,
			nodeIntegration: false,
			sandbox: true,
			transparent: true,
		},
	});
	shellView.setBackgroundColor("#00000000");
	options.mainWindow.contentView.addChildView(shellView, 0);

	let overlayOpen = false;
	const resize = (): void => {
		if (options.mainWindow.isDestroyed?.()) return;
		const bounds = options.mainWindow.contentView.getBounds();
		shellView.setBounds({ x: 0, y: 0, width: bounds.width, height: bounds.height });
		shellView.setVisible(true);
	};
	options.mainWindow.contentView.on("bounds-changed", resize);

	const forceSurfaceRefresh = (): void => {
		if (options.mainWindow.isDestroyed?.()) return;
		const bounds = options.mainWindow.contentView.getBounds();
		if (bounds.width <= 0 || bounds.height <= 0) return;
		shellView.setBounds({ x: 0, y: 0, width: bounds.width, height: Math.max(1, bounds.height - 1) });
		setTimeout(() => {
			if (options.mainWindow.isDestroyed?.()) return;
			const current = options.mainWindow.contentView.getBounds();
			shellView.setBounds({ x: 0, y: 0, width: current.width, height: current.height });
		}, 0);
	};

	const placeShell = (open: boolean, refresh = true): void => {
		if (open) {
			options.mainWindow.contentView.addChildView(shellView);
		} else {
			options.mainWindow.contentView.addChildView(shellView, 0);
		}
		if (open && refresh && options.platform === "darwin") forceSurfaceRefresh();
	};

	const setOverlayOpen = (open: boolean): void => {
		if (overlayOpen === open) return;
		overlayOpen = open;
		placeShell(open, options.hasLivePageBelow?.() ?? true);
	};

	const restackShell = (): void => {
		if (!overlayOpen || options.mainWindow.isDestroyed?.()) return;
		placeShell(true);
	};

	resize();

	return {
		shellView,
		shellWebContents: shellView.webContents,
		setOverlayOpen,
		restackShell,
		resize,
		dispose: () => {
			try {
				options.mainWindow.contentView.removeListener("bounds-changed", resize);
				options.mainWindow.contentView.removeChildView(shellView);
			} catch {
			}
			try {
				shellView.webContents.close();
			} catch {
			}
		},
	};
}
