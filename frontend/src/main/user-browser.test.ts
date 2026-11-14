import { describe, expect, it, vi } from "vitest";
import { createUserBrowser, type UserBrowserDeps } from "./user-browser";

function deps(overrides: Partial<UserBrowserDeps> & { present?: string[] }): UserBrowserDeps & { run: ReturnType<typeof vi.fn>; openExternal: ReturnType<typeof vi.fn> } {
	const present = new Set(overrides.present ?? []);
	return {
		platform: "darwin",
		home: "/Users/me",
		defaultBrowserName: () => "Safari",
		exists: async (file: string) => present.has(file),
		...overrides,
		run: (overrides.run as ReturnType<typeof vi.fn>) ?? vi.fn(async () => ""),
		openExternal: (overrides.openExternal as ReturnType<typeof vi.fn>) ?? vi.fn(async () => undefined),
	} as never;
}

const url = "https://example.com/a?b=1&c='x'";

describe("user browser", () => {
	it("opens Chrome on macOS by bundle id, with the URL as an argument", async () => {
		const d = deps({ present: ["/Applications/Google Chrome.app"] });
		const browser = createUserBrowser(d);
		expect(await browser.describe()).toEqual({ name: "Chrome", chrome: true });
		expect(await browser.open(url)).toEqual({ name: "Chrome", chrome: true });
		expect(d.run).toHaveBeenCalledWith("/usr/bin/open", ["-b", "com.google.Chrome", url]);
		expect(d.openExternal).not.toHaveBeenCalled();
	});

	it("finds Chrome installed per user on macOS", async () => {
		const d = deps({ present: ["/Users/me/Applications/Google Chrome.app"] });
		expect(await createUserBrowser(d).describe()).toEqual({ name: "Chrome", chrome: true });
	});

	it("falls back to the default browser, named, when Chrome is missing", async () => {
		const d = deps({ defaultBrowserName: () => "Firefox" });
		const browser = createUserBrowser(d);
		expect(await browser.describe()).toEqual({ name: "Firefox", chrome: false });
		expect(await browser.open(url)).toEqual({ name: "Firefox", chrome: false });
		expect(d.openExternal).toHaveBeenCalledWith(url);
	});

	it("falls back to the default browser when Chrome fails to start", async () => {
		const d = deps({ present: ["/Applications/Google Chrome.app"], run: vi.fn(async () => { throw new Error("gone"); }) });
		expect(await createUserBrowser(d).open(url)).toEqual({ name: "Safari", chrome: false });
		expect(d.openExternal).toHaveBeenCalledWith(url);
	});

	it("finds Chrome on Windows through App Paths and launches the exe directly", async () => {
		const exe = "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe";
		const run = vi.fn(async (file: string, args: string[]) =>
			file === "reg" && args[1]!.startsWith("HKLM") ? `\r\n    (Default)    REG_SZ    ${exe}\r\n` : file === "reg" ? "" : "",
		);
		const d = deps({ platform: "win32", present: [exe], run });
		const browser = createUserBrowser(d);
		await browser.open(url);
		expect(run).toHaveBeenCalledWith(exe, [url]);
	});

	it("finds google-chrome on Linux", async () => {
		const d = deps({ platform: "linux", present: ["/usr/bin/google-chrome-stable"] });
		await createUserBrowser(d).open(url);
		expect(d.run).toHaveBeenCalledWith("/usr/bin/google-chrome-stable", [url]);
	});
});
