import { ipcMain } from "electron";
import { nodeCliShadowIo } from "./cli-link-fs";
import { inspectCliShadow } from "../shared/cli-shadow";

export function registerCliShadowIpc(input: {
	shellPath: () => Promise<string | undefined>;
	bundledPath: () => string | undefined;
}): void {
	const io = nodeCliShadowIo();
	ipcMain.handle("cliShadow:inspect", async () =>
		inspectCliShadow(io, {
			platform: process.platform,
			shellPath: await input.shellPath(),
			bundledPath: input.bundledPath(),
		}),
	);
}
