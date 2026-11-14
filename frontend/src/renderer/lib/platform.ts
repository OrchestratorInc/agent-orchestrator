type NavigatorWithUserAgentData = Navigator & { userAgentData?: { platform?: string } };

function navigatorPlatform(): string {
	if (typeof navigator === "undefined") return "";
	return (navigator as NavigatorWithUserAgentData).userAgentData?.platform ?? navigator.platform ?? "";
}

function navigatorUserAgent(): string {
	if (typeof navigator === "undefined") return "";
	return navigator.userAgent ?? "";
}

export function isMacPlatform(): boolean {
	return /Mac|iPod|iPhone|iPad/.test(navigatorUserAgent()) || /mac/i.test(navigatorPlatform());
}

export function isWindowsPlatform(): boolean {
	return /win/i.test(navigatorPlatform());
}

export function isLinuxPlatform(): boolean {
	return navigatorPlatform().toLowerCase().includes("linux");
}

/**
 * macOS only: the window reserves a traffic-light band above a full-height
 * sidebar, so windowed overlays (browser/files pop-outs) clear it. Every page
 * renders its own PageTopbar inside the framed center panel on all platforms;
 * the fixed TitlebarNav cluster carries the sidebar toggle and history arrows.
 */
export function hidesShellTopbar(): boolean {
	return isMacPlatform();
}
