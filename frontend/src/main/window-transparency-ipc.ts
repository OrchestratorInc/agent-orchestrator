import type { IpcMain, WebContents } from "electron";
import { AoError } from "../../../packages/shared/telemetry/ao-error";
import type { WindowTransparencyState } from "../shared/window-transparency";

export function registerWindowTransparencyIpc(options: {
	ipcMain: Pick<IpcMain, "handle">;
	isShell: (sender: WebContents) => boolean;
	read: () => WindowTransparencyState;
	set: (enabled: boolean) => WindowTransparencyState;
}): void {
	options.ipcMain.handle("theme:get-window-transparency", (event) => {
		if (!options.isShell(event.sender)) throw new AoError("WINDOW_TRANSPARENCY_UNTRUSTED_SENDER", "Untrusted window transparency request.");
		return options.read();
	});
	options.ipcMain.handle("theme:set-window-translucent", (event, enabled: unknown) => {
		if (!options.isShell(event.sender)) throw new AoError("WINDOW_TRANSPARENCY_UNTRUSTED_SENDER", "Untrusted window transparency request.");
		if (typeof enabled !== "boolean") throw new AoError("WINDOW_TRANSPARENCY_INVALID_REQUEST", "Window transparency must be a boolean.");
		return options.set(enabled);
	});
}
