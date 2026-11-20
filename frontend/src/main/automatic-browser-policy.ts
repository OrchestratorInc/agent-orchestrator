import type { AutomaticBrowserMode } from "../shared/ui-locale";

/** Apply the user's policy before an agent command reaches the browser host. */
export async function enforceAutomaticBrowserPolicy(
	mode: AutomaticBrowserMode,
	action: string,
	args: Record<string, unknown> | undefined,
	openExternal: (url: string) => Promise<unknown>,
): Promise<void> {
	if (action === "__destroy-session") return;
	if (mode === "internal") return;
	if (mode === "external" && (action === "open" || action === "tab-new") && typeof args?.url === "string") {
		const raw = args.url.trim();
		if (!raw || raw.startsWith("/") || /^[a-zA-Z]:[\\/]/.test(raw) || /^file:/i.test(raw)) {
			throw Object.assign(new Error("Only HTTP and HTTPS URLs can be opened externally"), { code: "INVALID_ARGUMENT" });
		}
		const localhost = /^(localhost|127(?:\.\d{1,3}){3}|0\.0\.0\.0|\[::1\])(?::\d+)?(?:[/?#]|$)/i.test(raw);
		const host = raw.split(/[/?#]/, 1)[0];
		const looksLikeHost = (host.startsWith("[") && host.includes("]")) || /:\d+$/.test(host) || host.includes(".");
		const hasExplicitScheme = /^[a-zA-Z][a-zA-Z\d+.-]*:/.test(raw);
		let candidate = raw;
		if (!/^https?:\/\//i.test(raw)) {
			if (localhost) candidate = `http://${raw}`;
			else if (!hasExplicitScheme && !/\s/.test(raw) && looksLikeHost) candidate = `https://${raw}`;
		}
		let url: URL;
		try { url = new URL(candidate); } catch { throw Object.assign(new Error("A valid HTTP or HTTPS URL is required"), { code: "INVALID_ARGUMENT" }); }
		if (url.protocol !== "http:" && url.protocol !== "https:") throw Object.assign(new Error("Only HTTP and HTTPS URLs can be opened externally"), { code: "INVALID_ARGUMENT" });
		await openExternal(url.toString());
		throw Object.assign(new Error("Opened in the external browser; agent browser automation is unavailable in this mode"), { code: "BROWSER_AUTOMATION_DISABLED" });
	}
	throw Object.assign(new Error("Agent browser automation is disabled by the automatic browser setting"), { code: "BROWSER_AUTOMATION_DISABLED" });
}
