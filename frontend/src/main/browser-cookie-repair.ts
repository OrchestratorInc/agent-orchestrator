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

function sameCookie(a: RepairCookie, b: RepairCookie): boolean {
	return a.name === b.name && a.domain === b.domain && (a.path ?? "/") === (b.path ?? "/");
}

/**
 * Setting an expired cookie deletes exactly that name, domain and path. It never
 * touches the site's real cookie, so a failure or crash here cannot sign anyone
 * out. cookies.remove would also take every other cookie the URL can see.
 */
function expire(session: RepairSession, cookie: RepairCookie): Promise<void> {
	return session.cookies.set({
		url: urlOf(cookie),
		name: cookie.name,
		value: "",
		...(cookie.hostOnly ? {} : { domain: cookie.domain }),
		path: cookie.path || "/",
		secure: cookie.secure === true,
		httpOnly: cookie.httpOnly === true,
		expirationDate: 1,
	});
}

export async function repairCookies(session: RepairSession): Promise<RepairResult> {
	const cookies = await session.cookies.get({});
	const antiBot = cookies.filter((item) => isAntiBotCookie(item.name));
	const widened = cookies.filter(
		(item) =>
			!isAntiBotCookie(item.name) &&
			item.domain?.startsWith(".") &&
			cookies.some((other) => other.hostOnly && other.name === item.name && other.domain === hostOf(item) && (other.path ?? "/") === (item.path ?? "/")),
	);
	for (const cookie of [...antiBot, ...widened]) await expire(session, cookie).catch(() => undefined);

	// Only what is really gone counts, so a profile is never marked repaired early.
	const after = await session.cookies.get({});
	const gone = (cookie: RepairCookie) => !after.some((other) => sameCookie(other, cookie));
	const repaired = { antiBot: antiBot.filter(gone).length, duplicates: widened.filter(gone).length };
	return { ...repaired, failed: antiBot.length + widened.length - repaired.antiBot - repaired.duplicates };
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
