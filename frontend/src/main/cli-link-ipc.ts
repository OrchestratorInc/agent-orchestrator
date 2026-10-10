import { ipcMain } from "electron";
import path from "node:path";
import { nodeCliLinkIo } from "./cli-link-fs";
import { inspectCliLink, linkCli, unlinkCli, type CliLinkRequest } from "../shared/cli-link";
import { bundledDaemonBinaryName } from "../shared/daemon-launch";

let developerMode = false;

export function setCliLinkDeveloperMode(enabled: boolean): void {
	developerMode = enabled;
}

export function registerCliLinkIpc(input: {
	isPackaged: () => boolean;
	resourcesPath: string;
	appImage: string | undefined;
	dataDir: () => string;
}): void {
	const request = (confirmReplacement = false): CliLinkRequest => ({
		developerMode,
		platform: process.platform,
		isPackaged: input.isPackaged(),
		appImage: input.appImage,
		appCliPath: path.join(input.resourcesPath, "daemon", bundledDaemonBinaryName(process.platform)),
		linkDirectory: "/usr/local/bin",
		dataDir: input.dataDir(),
		allowSystemLinkDir: true,
		confirmReplacement,
	});
	const io = nodeCliLinkIo();
	ipcMain.handle("cliLink:setEnabled", (_event, enabled: unknown) => {
		setCliLinkDeveloperMode(enabled === true);
	});
	ipcMain.handle("cliLink:inspect", () => inspectCliLink(io, request()));
	ipcMain.handle("cliLink:link", (_event, confirm: unknown) => linkCli(io, request(confirm === true)));
	ipcMain.handle("cliLink:unlink", (_event, confirm: unknown) => unlinkCli(io, request(confirm === true)));
}
