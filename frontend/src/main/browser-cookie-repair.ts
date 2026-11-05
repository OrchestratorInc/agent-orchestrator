// Imported profiles can hold cookies that keep sites failing, and updating AO
// must fix them without the user re-importing:
//   - imports before #6075 widened host-only cookies to domain cookies. Once the
//     site sets its real one, the browser sends two with different values, and
//     Google answers with its cookie error page;
//   - imports copied anti-bot cookies, which only work in the browser that
//     earned them, so sites keep challenging.
import Database from "better-sqlite3";
import { readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import { isAntiBotCookie } from "./browser-profile-import";

const STATE_FILE = "browser-cookie-repair.json";
// An import writes cookies far faster than sites set them: a real imported
// profile holds 407 created within 21 ms.
const IMPORT_BURST_GAP_MICROSECONDS = 100_000;
const IMPORT_BURST_MIN_COOKIES = 20;

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

export function cookieKey(cookie: Pick<RepairCookie, "domain" | "name" | "path">): string {
	return `${cookie.domain ?? ""}\0${cookie.name}\0${cookie.path || "/"}`;
}

/**
 * Old imports left no record, but the cookie store shows them: nothing else
 * creates this many cookies this fast. Unreadable means nothing is reported, so
 * a duplicate is only ever removed on evidence.
 */
export function importedCookieKeys(databaseFile: string, log?: (message: string) => void): Set<string> {
	const keys = new Set<string>();
	try {
		const database = new Database(databaseFile, { readonly: true, fileMustExist: true, timeout: 250 });
		try {
			const rows = database.prepare("SELECT host_key, name, path, creation_utc FROM cookies ORDER BY creation_utc").all() as Array<{
				host_key: string;
				name: string;
				path: string;
				creation_utc: number | bigint;
			}>;
			let burst: typeof rows = [];
			const flush = () => {
				if (burst.length >= IMPORT_BURST_MIN_COOKIES) {
					for (const row of burst) keys.add(cookieKey({ domain: row.host_key, name: row.name, path: row.path }));
				}
				burst = [];
			};
			for (const row of rows) {
				const previous = burst.at(-1);
				if (previous && Number(row.creation_utc) - Number(previous.creation_utc) > IMPORT_BURST_GAP_MICROSECONDS) flush();
				burst.push(row);
			}
			flush();
		} finally {
			database.close();
		}
	} catch (error) {
		log?.(`AO: could not read a browser profile's cookie store, so its duplicates stay: ${String(error)}`);
	}
	return keys;
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

export async function repairCookies(session: RepairSession, importedKeys: ReadonlySet<string>): Promise<RepairResult> {
	const cookies = await session.cookies.get({});
	const antiBot = cookies.filter((item) => isAntiBotCookie(item.name));
	// A site can set both forms itself. It is only import damage when the domain
	// copy came from an import and the site's real cookie arrived later.
	const widened = cookies.filter(
		(item) =>
			!isAntiBotCookie(item.name) &&
			item.domain?.startsWith(".") &&
			importedKeys.has(cookieKey(item)) &&
			cookies.some(
				(other) =>
					other.hostOnly &&
					other.name === item.name &&
					other.domain === hostOf(item) &&
					(other.path ?? "/") === (item.path ?? "/") &&
					!importedKeys.has(cookieKey(other)),
			),
	);
	for (const cookie of [...antiBot, ...widened]) await expire(session, cookie).catch(() => undefined);

	// Only what is really gone counts, so a profile is never marked repaired early.
	const after = await session.cookies.get({});
	const gone = (cookie: RepairCookie) => !after.some((other) => sameCookie(other, cookie));
	const repaired = { antiBot: antiBot.filter(gone).length, duplicates: widened.filter(gone).length };
	return { ...repaired, failed: antiBot.length + widened.length - repaired.antiBot - repaired.duplicates };
}

type RepairOptions = {
	stateDir: string;
	profileIds: string[];
	fromProfile: (profileId: string) => RepairSession;
	cookieDatabase: (profileId: string) => string;
	log?: (message: string) => void;
};

const runs = new Map<string, Promise<void>>();

type RepairState = { kind: "first-run" } | { kind: "unreadable" } | { kind: "ready"; pending: string[] };

async function readState(file: string): Promise<RepairState> {
	let text: string;
	try {
		text = await readFile(file, "utf8");
	} catch (error) {
		return (error as NodeJS.ErrnoException).code === "ENOENT" ? { kind: "first-run" } : { kind: "unreadable" };
	}
	try {
		const pending = (JSON.parse(text) as { pending?: unknown }).pending;
		return { kind: "ready", pending: Array.isArray(pending) ? pending.filter((id): id is string => typeof id === "string") : [] };
	} catch {
		return { kind: "unreadable" };
	}
}

async function writeState(file: string, pending: string[]): Promise<void> {
	const temporary = `${file}.${process.pid}.tmp`;
	await writeFile(temporary, `${JSON.stringify({ version: 2, pending })}\n`, { mode: 0o600 });
	await rename(temporary, file);
}

/**
 * Repairs the profiles that existed when this version first ran, once each. Later
 * profiles earned their own cookies, so they are never touched. Failures are logged and not
 * retried: repeating this at every launch would wipe freshly earned anti-bot
 * cookies. Never throws, so a failed repair cannot stop AO starting.
 */
export function repairBrowserProfileCookies(options: RepairOptions): Promise<void> {
	// Reopening the window can start this during a run. They share one run.
	const file = path.join(options.stateDir, STATE_FILE);
	const running = runs.get(file);
	if (running) return running;
	const run = runRepair(file, options).finally(() => runs.delete(file));
	runs.set(file, run);
	return run;
}

async function runRepair(file: string, options: RepairOptions): Promise<void> {
	const state = await readState(file);
	if (state.kind === "unreadable") {
		options.log?.("AO: browser profile cookie repair skipped: its state file is unreadable");
		return;
	}
	const pending = state.kind === "first-run" ? [...options.profileIds] : state.pending;
	try {
		if (state.kind === "first-run") await writeState(file, pending);
		for (const profileId of [...pending]) {
			if (options.profileIds.includes(profileId)) {
				try {
					// Read before opening the session, which takes over the cookie store.
					const importedKeys = importedCookieKeys(options.cookieDatabase(profileId), options.log);
					const result = await repairCookies(options.fromProfile(profileId), importedKeys);
					options.log?.(`AO: repaired browser profile cookies: ${result.antiBot} anti-bot, ${result.duplicates} duplicates, ${result.failed} failed`);
				} catch (error) {
					options.log?.(`AO: browser profile cookie repair failed: ${String(error)}`);
				}
			}
			pending.splice(pending.indexOf(profileId), 1);
			await writeState(file, pending);
		}
	} catch (error) {
		// Without a record the next launch could repeat this, so stop here.
		options.log?.(`AO: could not record browser profile cookie repair: ${String(error)}`);
	}
}
