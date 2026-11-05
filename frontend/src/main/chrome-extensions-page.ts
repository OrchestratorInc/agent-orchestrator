// chrome:// URLs have no OS handler, so shell.openExternal cannot open them.
// Chrome accepts one when it is launched with the URL as an argument.
const EXTENSIONS_PAGE = "chrome://extensions";

export type ChromeCommandRunner = (file: string, args: string[]) => Promise<unknown>;

export function chromeExtensionsPageCommand(platform: NodeJS.Platform): [string, string[]] {
	if (platform === "darwin") return ["open", ["-a", "Google Chrome", EXTENSIONS_PAGE]];
	if (platform === "win32") return ["cmd", ["/c", "start", "", "chrome", EXTENSIONS_PAGE]];
	return ["google-chrome", [EXTENSIONS_PAGE]];
}

export async function openChromeExtensionsPage(platform: NodeJS.Platform, run: ChromeCommandRunner): Promise<void> {
	const [file, args] = chromeExtensionsPageCommand(platform);
	await run(file, args);
}
