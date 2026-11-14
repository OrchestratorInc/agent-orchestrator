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

export function hidesShellTopbar(): boolean {
	return isMacPlatform();
}
