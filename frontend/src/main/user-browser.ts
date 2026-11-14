// Opens a page in the person's own browser: Google Chrome when it is
// installed (where their sign-ins and passkeys are, and where Chrome mode
// agents work), otherwise their default browser. URLs are passed as process
// arguments, never through a shell, so no URL can run a command.

import { execFile } from "node:child_process";
import { access } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

export type UserBrowser = {
	/** Shown to the person: "Chrome", or the default browser's own name. */
	name: string;
	/** True when the page goes to Google Chrome specifically. */
	chrome: boolean;
};

export type UserBrowserDeps = {
	platform: NodeJS.Platform;
	/** Electron's shell.openExternal. */
	openExternal: (url: string) => Promise<void>;
	/** Electron's app.getApplicationNameForProtocol. */
	defaultBrowserName: () => string;
	run?: (file: string, args: string[]) => Promise<string>;
	exists?: (file: string) => Promise<boolean>;
	home?: string;
};

const defaultRun = (file: string, args: string[]) =>
	new Promise<string>((resolve, reject) => {
		execFile(file, args, { timeout: 5_000, windowsHide: true }, (error, stdout) => (error ? reject(error) : resolve(String(stdout))));
	});

const defaultExists = (file: string) =>
	access(file).then(
		() => true,
		() => false,
	);

/** Where Chrome is, as something to launch with the URL as its last argument. */
async function findChrome(deps: UserBrowserDeps): Promise<{ file: string; args: string[] } | undefined> {
	const run = deps.run ?? defaultRun;
	const exists = deps.exists ?? defaultExists;
	const home = deps.home ?? os.homedir();
	switch (deps.platform) {
		case "darwin": {
			const bundles = ["/Applications/Google Chrome.app", path.join(home, "Applications", "Google Chrome.app")];
			for (const bundle of bundles) if (await exists(bundle)) return { file: "/usr/bin/open", args: ["-b", "com.google.Chrome"] };
			return undefined;
		}
		case "win32": {
			for (const hive of ["HKCU", "HKLM"]) {
				const out = await run("reg", ["query", `${hive}\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\App Paths\\chrome.exe`, "/ve"]).catch(() => "");
				const match = /REG_SZ\s+(.+\.exe)/i.exec(out);
				if (match && (await exists(match[1]!.trim()))) return { file: match[1]!.trim(), args: [] };
			}
			return undefined;
		}
		default: {
			for (const name of ["google-chrome", "google-chrome-stable"]) {
				for (const dir of ["/usr/bin", "/usr/local/bin", "/opt/google/chrome"]) {
					const file = path.join(dir, name);
					if (await exists(file)) return { file, args: [] };
				}
			}
			return undefined;
		}
	}
}

export function createUserBrowser(deps: UserBrowserDeps): {
	describe(): Promise<UserBrowser>;
	open(url: string): Promise<UserBrowser>;
} {
	const run = deps.run ?? defaultRun;
	let chrome: Promise<{ file: string; args: string[] } | undefined> | undefined;
	const locate = () => (chrome ??= findChrome(deps).catch(() => undefined));
	const fallback = (): UserBrowser => {
		const name = deps.defaultBrowserName().replace(/^Google /, "").trim();
		return { name: name || "", chrome: /chrome/i.test(name) };
	};
	return {
		async describe() {
			return (await locate()) ? { name: "Chrome", chrome: true } : fallback();
		},
		async open(url) {
			const found = await locate();
			if (found) {
				try {
					await run(found.file, [...found.args, url]);
					return { name: "Chrome", chrome: true };
				} catch {
					// Chrome moved or failed to start: the default browser still works.
				}
			}
			await deps.openExternal(url);
			return fallback();
		},
	};
}
