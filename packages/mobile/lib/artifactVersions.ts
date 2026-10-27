/**
 * An artifact file's version history, as the desktop's History menu shows it
 * (frontend ArtifactHistory). The daemon keeps up to 50 versions per file,
 * newest first; the first one is the file's current content.
 */

/** One kept version of an artifact file. */
export type ArtifactVersionSummary = {
	version: number;
	size: number;
	/** Who wrote it: the agent, or the user through a save or restore. */
	source: "agent" | "user";
	/** The version a restore wrote back; absent otherwise. */
	restoredFrom?: number;
	/** Best effort: the agent running when the version was recorded. */
	harness?: string;
	createdAt: string;
};

/** One version with its text, cut to the first 256 KiB when `truncated`. */
export type ArtifactVersion = ArtifactVersionSummary & { content: string; truncated: boolean };

/** Reads the wire `versions` list, dropping anything malformed. */
export function parseVersionSummaries(raw: unknown): ArtifactVersionSummary[] {
	if (!Array.isArray(raw)) return [];
	return raw.flatMap((item) => {
		const summary = parseVersionSummary(item);
		return summary ? [summary] : [];
	});
}

export function parseVersionSummary(raw: unknown): ArtifactVersionSummary | undefined {
	if (!raw || typeof raw !== "object") return undefined;
	const { version, size, source, restoredFrom, harness, createdAt } = raw as Record<string, unknown>;
	if (typeof version !== "number" || !Number.isInteger(version) || version < 1) return undefined;
	return {
		version,
		size: typeof size === "number" && size >= 0 ? size : 0,
		source: source === "user" ? "user" : "agent",
		...(typeof restoredFrom === "number" && restoredFrom > 0 ? { restoredFrom } : {}),
		...(typeof harness === "string" && harness ? { harness } : {}),
		createdAt: typeof createdAt === "string" ? createdAt : "",
	};
}

/** One version's text, from the version read route. */
export function parseVersion(raw: unknown): ArtifactVersion | undefined {
	const summary = parseVersionSummary(raw);
	if (!summary) return undefined;
	const { content, truncated } = raw as Record<string, unknown>;
	return { ...summary, content: typeof content === "string" ? content : "", truncated: truncated === true };
}

const DAY_MS = 86_400_000;

/**
 * "Today, 8:31 PM", "Yesterday, 8:31 PM", else "Oct 11, 8:31 PM" (with the
 * year when it is not this one). Written out rather than through
 * Intl.RelativeTimeFormat, which Hermes does not have.
 */
export function formatVersionTime(isoDate: string, now: Date = new Date(), locale?: string): string {
	const date = new Date(isoDate);
	if (Number.isNaN(date.getTime())) return "";
	const time = date.toLocaleTimeString(locale, { hour: "numeric", minute: "2-digit" });
	const dayStart = (value: Date) => new Date(value.getFullYear(), value.getMonth(), value.getDate()).getTime();
	const days = Math.round((dayStart(now) - dayStart(date)) / DAY_MS);
	const day = days === 0
		? "Today"
		: days === 1
			? "Yesterday"
			: date.toLocaleDateString(locale, { day: "numeric", month: "short", ...(date.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }) });
	return `${day}, ${time}`;
}

/**
 * "Restored from Today, 8:31 PM" for a version a restore wrote; the version it
 * came from may have been pruned since.
 */
export function restoredNote(version: ArtifactVersionSummary, versions: readonly ArtifactVersionSummary[], now?: Date, locale?: string): string | undefined {
	if (!version.restoredFrom) return undefined;
	const source = versions.find((candidate) => candidate.version === version.restoredFrom);
	return source ? `Restored from ${formatVersionTime(source.createdAt, now, locale)}` : "Restored from an earlier version";
}

/** Who wrote a version: the user, or an agent (the session's, when the version predates the record). */
export function versionAuthor(version: ArtifactVersionSummary, fallbackHarness?: string | null): { kind: "user" } | { kind: "agent"; harness?: string } {
	if (version.source === "user") return { kind: "user" };
	return { kind: "agent", harness: version.harness || fallbackHarness || undefined };
}
