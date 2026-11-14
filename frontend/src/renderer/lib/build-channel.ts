export function isNightlyBuild(version?: string): boolean {
	return version?.includes("-nightly.") ?? false;
}

export function isCommandPaletteEnabled(version?: string, isDev: boolean = import.meta.env.DEV): boolean {
	return isDev || isNightlyBuild(version);
}

export function isTeamsEnabled(
	version?: string,
	isDev: boolean = import.meta.env.DEV,
	flag: string | undefined = import.meta.env.VITE_AO_TEAMS,
): boolean {
	if (flag === "on") return true;
	if (flag === "off") return false;
	return isDev || isNightlyBuild(version);
}

export function parseNightlyVersion(version?: string): { base: string; builtAt: Date } | null {
	const match = /^(\d+\.\d+\.\d+)-nightly\.(\d{4})(\d{2})(\d{2})(\d{2})(\d{2})(?:[.+]|$)/.exec(version ?? "");
	if (!match) return null;
	const [, base, year, month, day, hour, minute] = match;
	const builtAt = new Date(
		Date.UTC(Number(year), Number(month) - 1, Number(day), Number(hour), Number(minute)),
	);
	if (Number.isNaN(builtAt.getTime()) || builtAt.getUTCMonth() !== Number(month) - 1) return null;
	return { base, builtAt };
}
