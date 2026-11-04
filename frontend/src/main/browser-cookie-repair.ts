// Imported profiles can hold cookies that keep sites failing, and updating AO
// must fix them without the user re-importing:
//   - imports before #6075 widened host-only cookies to domain cookies. Once the
//     site sets its real one, the browser sends two with different values, and
//     Google answers with its cookie error page;
//   - imports copied anti-bot cookies, which only work in the browser that
//     earned them, so sites keep challenging.
import { readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import { isAntiBotCookie } from "./browser-profile-import";

const STATE_FILE = "browser-cookie-repair.json";

export type RepairCookie = {
	domain?: string;
	name: string;
	value: string;
	path?: string;
	secure?: boolean;
	httpOnly?: boolean;
	hostOnly?: boolean;
	sameSite?: "unspecified" | "no_restriction" | "lax" | "strict";
	expirationDate?: number;
};

export type RepairSession = {
	cookies: {
		get(filter: Record<string, never>): Promise<RepairCookie[]>;
		remove(url: string, name: string): Promise<void>;
		set(details: {
			url: string;
			name: string;
			value: string;
			domain?: string;
			path?: string;
			secure?: boolean;
			httpOnly?: boolean;
			sameSite?: RepairCookie["sameSite"];
			expirationDate?: number;
		}): Promise<void>;
	};
};

export type RepairResult = { antiBot: number; duplicates: number; failed: number };

function hostOf(cookie: RepairCookie): string {
	return (cookie.domain ?? "").replace(/^\.+/, "");
}

function urlOf(cookie: RepairCookie): string {
	return `${cookie.secure ? "https" : "http"}://${hostOf(cookie)}${cookie.path || "/"}`;
}

function appliesTo(cookie: RepairCookie, host: string): boolean {
	const domain = cookie.domain ?? "";
	return domain.startsWith(".") ? `.${host}`.endsWith(domain) : domain === host;
}

export async function repairCookies(session: RepairSession): Promise<RepairResult> {
	const cookies = await session.cookies.get({});
	const result: RepairResult = { antiBot: 0, duplicates: 0, failed: 0 };

	for (const cookie of cookies.filter((item) => isAntiBotCookie(item.name))) {
		try {
			await session.cookies.remove(urlOf(cookie), cookie.name);
			result.antiBot += 1;
		} catch {
			result.failed += 1;
		}
	}

	const widened = cookies.filter(
		(item) =>
			!isAntiBotCookie(item.name) &&
			item.domain?.startsWith(".") &&
			cookies.some((other) => other.hostOnly && other.name === item.name && other.domain === hostOf(item) && (other.path ?? "/") === (item.path ?? "/")),
	);
	// Restore only from the final jar: a removed duplicate must not come back
	// while another one on an overlapping domain is being repaired.
	const remaining = cookies.filter((item) => !isAntiBotCookie(item.name) && !widened.includes(item));
	for (const cookie of widened) {
		try {
			// Removal takes every cookie this URL can see with that name, so put
			// the others back: the site's real one and any parent-domain cookie.
			const host = hostOf(cookie);
			const keep = remaining.filter((other) => other.name === cookie.name && appliesTo(other, host));
			await session.cookies.remove(urlOf(cookie), cookie.name);
			result.duplicates += 1;
			for (const other of keep) {
				try {
					await session.cookies.set({
						url: urlOf(other),
						name: other.name,
						value: other.value,
						...(other.hostOnly ? {} : { domain: other.domain }),
						path: other.path || "/",
						secure: other.secure === true,
						httpOnly: other.httpOnly === true,
						...(other.sameSite ? { sameSite: other.sameSite } : {}),
						...(other.expirationDate !== undefined ? { expirationDate: other.expirationDate } : {}),
					});
				} catch {
					result.failed += 1;
				}
			}
		} catch {
			result.failed += 1;
		}
	}
	return result;
}

async function readRepaired(file: string): Promise<string[]> {
	try {
		const parsed: unknown = JSON.parse(await readFile(file, "utf8"));
		const repaired = (parsed as { repaired?: unknown }).repaired;
		return Array.isArray(repaired) ? repaired.filter((id): id is string => typeof id === "string") : [];
	} catch {
		return [];
	}
}

/** Repairs each profile once. Never throws: a failed repair must not stop AO starting. */
export async function repairBrowserProfileCookies(options: {
	stateDir: string;
	profileIds: string[];
	fromProfile: (profileId: string) => RepairSession;
	log?: (message: string) => void;
}): Promise<void> {
	const file = path.join(options.stateDir, STATE_FILE);
	const repaired = await readRepaired(file);
	const pending = options.profileIds.filter((id) => !repaired.includes(id));
	if (pending.length === 0) return;
	for (const profileId of pending) {
		try {
			const result = await repairCookies(options.fromProfile(profileId));
			options.log?.(`AO: repaired browser profile cookies: ${result.antiBot} anti-bot, ${result.duplicates} duplicates, ${result.failed} failed`);
			if (result.failed === 0) repaired.push(profileId);
		} catch (error) {
			options.log?.(`AO: browser profile cookie repair failed: ${String(error)}`);
		}
	}
	try {
		const temporary = `${file}.${process.pid}.tmp`;
		await writeFile(temporary, `${JSON.stringify({ version: 1, repaired })}\n`, { mode: 0o600 });
		await rename(temporary, file);
	} catch (error) {
		options.log?.(`AO: could not record browser profile cookie repair: ${String(error)}`);
	}
}
