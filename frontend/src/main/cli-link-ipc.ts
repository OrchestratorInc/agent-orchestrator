import { ipcMain } from "electron";
import { homedir } from "node:os";
import path from "node:path";
import { nodeCliLinkIo } from "./cli-link-fs";
import { CLI_LINK_DIRECTORY, inspectCliLink, linkCli, selectCliLinkDirectory, unlinkCli, type CliLinkRequest } from "../shared/cli-link";
import { bundledDaemonBinaryName } from "../shared/daemon-launch";

let developerMode = false;

export function setCliLinkDeveloperMode(enabled: boolean): void {
	developerMode = enabled;
}

export function registerCliLinkIpc(input: {
	isPackaged: () => boolean;
	resourcesPath: string;
	appImage: string | undefined;
	dataDir: string;
	appVersion: string;
	shellPath: () => string | undefined;
}): void {
	const io = nodeCliLinkIo();
	const request = async (confirmReplacement = false, expectedTarget?: string | null): Promise<CliLinkRequest> => {
		const shellPath = input.shellPath();
		return {
			developerMode,
			platform: process.platform,
			isPackaged: input.isPackaged(),
			appImage: input.appImage,
			appCliPath: path.join(input.resourcesPath, "daemon", bundledDaemonBinaryName(process.platform)),
			linkDirectory: await selectCliLinkDirectory(io, input.dataDir, shellPath, [
				CLI_LINK_DIRECTORY,
				...(process.platform === "darwin" ? ["/opt/homebrew/bin"] : []),
				path.join(homedir(), ".local", "bin"),
				path.join(homedir(), "bin"),
			]),
			dataDir: input.dataDir,
			appVersion: input.appVersion,
			shellPath,
			allowSystemLinkDir: true,
			confirmReplacement,
			expectedTarget,
		};
	};
	ipcMain.handle("cliLink:setEnabled", (_event, enabled: unknown) => {
		setCliLinkDeveloperMode(enabled === true);
	});
	ipcMain.handle("cliLink:inspect", async () => inspectCliLink(io, await request()));
	ipcMain.handle("cliLink:link", async (_event, confirm: unknown, target: unknown) => linkCli(io, await request(confirm === true, typeof target === "string" || target === null ? target : undefined)));
	ipcMain.handle("cliLink:unlink", async (_event, confirm: unknown, target: unknown) => unlinkCli(io, await request(confirm === true, typeof target === "string" || target === null ? target : undefined)));
}
