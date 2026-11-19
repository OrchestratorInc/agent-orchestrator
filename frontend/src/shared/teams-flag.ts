/**
 * VITE_AO_TEAMS, read when the app is built. "on" gives every build Teams
 * (sharing with people and teams, the Teams settings page, shared-with-you
 * notices, access tags), "off" gives none, and unset leaves it to the build:
 * dev and nightly builds have Teams, stable ones don't. Anything else is a
 * mistake, so the build stops on it rather than guess.
 */
export type TeamsFlag = "on" | "off" | "";

export function parseTeamsFlag(raw: string | undefined): TeamsFlag {
	const value = raw?.trim().toLowerCase() ?? "";
	if (value === "" || value === "on" || value === "off") return value;
	throw new Error(`VITE_AO_TEAMS must be "on" or "off", or unset; got ${JSON.stringify(raw)}.`);
}
