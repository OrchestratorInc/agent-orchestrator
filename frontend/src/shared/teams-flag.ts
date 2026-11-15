export type TeamsFlag = "on" | "off" | "";

export function parseTeamsFlag(raw: string | undefined): TeamsFlag {
	const value = raw?.trim().toLowerCase() ?? "";
	if (value === "" || value === "on" || value === "off") return value;
	throw new Error(`VITE_AO_TEAMS must be "on" or "off", or unset; got ${JSON.stringify(raw)}.`);
}
