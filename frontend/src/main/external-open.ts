const APP_EXTERNAL_PROTOCOLS = new Set(["http:", "https:", "mailto:"]);
// macOS System Settings panes the renderer may open, matched exactly: the
// Privacy & Security panes where Cua Driver's permissions are managed.
const APP_EXTERNAL_EXACT_URLS = new Set([
	"x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility",
	"x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture",
]);

export type ExternalOpener = {
	openExternal: (url: string) => Promise<unknown>;
};

export function isAllowedAppExternalURL(rawUrl: string): boolean {
	if (APP_EXTERNAL_EXACT_URLS.has(rawUrl)) return true;
	try {
		const url = new URL(rawUrl);
		return APP_EXTERNAL_PROTOCOLS.has(url.protocol);
	} catch {
		return false;
	}
}

export async function openAllowedAppExternalURL(rawUrl: string, opener: ExternalOpener): Promise<void> {
	if (!isAllowedAppExternalURL(rawUrl)) {
		throw new Error("Unsupported external URL");
	}
	await opener.openExternal(rawUrl);
}
