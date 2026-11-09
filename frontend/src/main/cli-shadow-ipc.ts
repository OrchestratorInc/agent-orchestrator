import { ipcMain } from "electron";
import { nodeCliShadowIo } from "./cli-link-fs";
import { inspectCliShadow, replaceCliShadow, type CliShadowReplaceResult, type CliShadowRequest } from "../shared/cli-shadow";

export function registerCliShadowIpc(input: {
	shellPath: () => Promise<string | undefined>;
	bundledPath: () => string | undefined;
	isPackaged: () => boolean;
	appImage: string | undefined;
	reveal: (path: string) => void;
}): void {
	const io = nodeCliShadowIo();
	const request = async (): Promise<CliShadowRequest> => ({
		platform: process.platform,
		shellPath: await input.shellPath(),
		bundledPath: input.bundledPath(),
		isPackaged: input.isPackaged(),
		appImage: input.appImage,
	});
	ipcMain.handle("cliShadow:inspect", async (_event, identity: unknown) => {
		return inspectCliShadow(io, { ...await request(), probeIdentity: typeof identity === "string" ? identity : undefined });
	});
	ipcMain.handle("cliShadow:replace", async (_event, path: unknown, target: unknown, identity: unknown): Promise<CliShadowReplaceResult> => {
		if (typeof path !== "string" || typeof target !== "string" || typeof identity !== "string") return { ok: false, code: "changed", message: "Nothing to replace." };
		return replaceCliShadow(io, await request(), { path, target, identity });
	});
	// Takes no path from the renderer: it only ever reveals the current other ao.
	ipcMain.handle("cliShadow:reveal", async () => {
		const status = await inspectCliShadow(io, await request());
		if (status.kind === "shadowed") input.reveal(status.shadowPath);
	});
}
