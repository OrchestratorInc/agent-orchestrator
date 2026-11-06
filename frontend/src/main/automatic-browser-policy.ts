import type { AutomaticBrowserMode } from "../shared/ui-locale";

/** Apply the user's policy before an agent command reaches the browser host. */
export async function enforceAutomaticBrowserPolicy(
	mode: AutomaticBrowserMode,
	action: string,
	args: Record<string, unknown> | undefined,
	openExternal: (url: string) => Promise<unknown>,
): Promise<void> {
	if (mode === "internal") return;
	if (mode === "external" && (action === "open" || action === "tab-new") && typeof args?.url === "string") {
		let url: URL;
		try { url = new URL(args.url); } catch { throw Object.assign(new Error("A valid HTTP or HTTPS URL is required"), { code: "INVALID_ARGUMENT" }); }
		if (url.protocol !== "http:" && url.protocol !== "https:") throw Object.assign(new Error("Only HTTP and HTTPS URLs can be opened externally"), { code: "INVALID_ARGUMENT" });
		await openExternal(url.toString());
		throw Object.assign(new Error("Opened in the external browser; agent browser automation is unavailable in this mode"), { code: "BROWSER_AUTOMATION_DISABLED" });
	}
	throw Object.assign(new Error("Agent browser automation is disabled by the automatic browser setting"), { code: "BROWSER_AUTOMATION_DISABLED" });
}
