import { BrowserWindow, shell } from "electron";
import { sharedArtifactAppURL } from "../../../packages/shared/artifacts/shared-artifact-link";

// Keep remote documents completely separate from the supervisor's preload,
// credentials and IPC. The viewer itself enforces access, including sign-in.
const windows = new Set<BrowserWindow>();

export function openSharedArtifact(raw: string): void {
	const url = sharedArtifactAppURL(raw);
	if (!url) return;
	const window = new BrowserWindow({
		title: "Shared artifact · Orchestrator",
		width: 1000,
		height: 800,
		webPreferences: {
			partition: "persist:shared-artifacts",
			nodeIntegration: false,
			contextIsolation: true,
			sandbox: true,
			webviewTag: false,
		},
	});
	windows.add(window);
	window.on("closed", () => windows.delete(window));
	const contents = window.webContents;
	contents.session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
	contents.session.setPermissionCheckHandler(() => false);
	const httpsOnly = (event: { preventDefault(): void }, target: string) => {
		try { if (new URL(target).protocol === "https:") return; } catch { /* refuse invalid URLs */ }
		event.preventDefault();
	};
	contents.on("will-navigate", httpsOnly);
	contents.on("will-redirect", httpsOnly);
	contents.setWindowOpenHandler(({ url: target }) => {
		try {
			if (new URL(target).protocol === "https:") void shell.openExternal(target).catch(() => {});
		} catch { /* refuse invalid URLs */ }
		return { action: "deny" };
	});
	void window.loadURL(url).catch(() => {
		if (!window.isDestroyed()) window.setTitle("Could not load shared artifact · Orchestrator");
	});
}
