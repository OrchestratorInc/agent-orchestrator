import type {
	CloudCpEventMention,
	CloudCpMentionPerson,
	CloudCpMentionTeam,
	CloudCpMentionToken,
} from "./cloud-cp/types";

// @mentions in shared Cloud sessions. A mention is a chip in the composer and
// in the timeline; it travels beside the message's text as a typed token
// ({kind, userId} or {kind, teamId}), and the agent reads only the text, where
// each chip is written as @Name.

/** People shown under "In this session" in the @ menu. */
export const IN_SESSION_LIMIT = 5;
/** At most this many people and teams in one message or steer (the contract's cap). */
export const MAX_MENTIONS = 20;

/** A mention chip in the composer: who it names, by id, and the name it shows. */
export interface ComposerMention {
	kind: "person" | "team";
	id: string;
	name: string;
}

/** A mention drawn in the timeline: as the event recorded it. */
export type TimelineMention = CloudCpEventMention;

/** The token a chip sends beside the text. */
export function mentionToken(mention: Pick<ComposerMention, "kind" | "id">): CloudCpMentionToken {
	return mention.kind === "person" ? { kind: "person", userId: mention.id } : { kind: "team", teamId: mention.id };
}

/** Each person or team once, in the order they were first mentioned. */
export function uniqueMentions<T extends Pick<ComposerMention, "kind" | "id">>(mentions: readonly T[]): T[] {
	const seen = new Set<string>();
	return mentions.filter((mention) => {
		const key = `${mention.kind}:${mention.id}`;
		if (seen.has(key)) return false;
		seen.add(key);
		return true;
	});
}

/** What a chip reads as in the text: `@` and the name. */
export function mentionText(mention: Pick<ComposerMention, "name">): string {
	return `@${mention.name}`;
}

/**
 * Text with each mention's `@Name` picked out, for drawing it as a chip:
 * where a mention starts the text or follows whitespace and ends where the
 * name does. Longer names win, so "@Chen Wei" is not read as "@Chen".
 */
export function splitByMentions<T extends { name: string }>(text: string, mentions: readonly T[]): Array<string | T> {
	if (mentions.length === 0 || !text.includes("@")) return [text];
	const ordered = [...mentions].filter((mention) => mention.name !== "").sort((a, b) => b.name.length - a.name.length);
	const parts: Array<string | T> = [];
	let plain = "";
	let index = 0;
	while (index < text.length) {
		if (text[index] === "@" && (index === 0 || /\s/.test(text[index - 1]!))) {
			const match = ordered.find((mention) => text.startsWith(mentionText(mention), index) &&
				!/[\p{L}\p{N}_]/u.test(text[index + mentionText(mention).length] ?? " "));
			if (match) {
				if (plain) parts.push(plain);
				plain = "";
				parts.push(match);
				index += mentionText(match).length;
				continue;
			}
		}
		plain += text[index];
		index += 1;
	}
	if (plain) parts.push(plain);
	return parts;
}

/** The mentions an event records, each with the name it had when sent; anything malformed is left out. */
export function parseEventMentions(value: unknown): TimelineMention[] {
	if (!Array.isArray(value)) return [];
	return value.flatMap((entry): TimelineMention[] => {
		if (!entry || typeof entry !== "object") return [];
		const mention = entry as Record<string, unknown>;
		if (mention.kind !== "person" && mention.kind !== "team") return [];
		if (typeof mention.id !== "string" || mention.id === "") return [];
		if (typeof mention.name !== "string" || mention.name === "") return [];
		return [{ kind: mention.kind, id: mention.id, name: mention.name }];
	});
}

/**
 * Whether a send was refused for a mention (422 validation_error on the
 * mentions field): someone who can't open the session, a guest naming someone
 * outside it, an unknown id, or too many. Nothing was sent.
 */
export function mentionRefusal(error: unknown): string | undefined {
	if (typeof error !== "object" || error === null) return undefined;
	const candidate = error as { status?: unknown; code?: unknown; details?: unknown };
	if (candidate.status !== 422 || candidate.code !== "validation_error") return undefined;
	const details = candidate.details;
	if (!details || typeof details !== "object" || (details as { field?: unknown }).field !== "mentions") return undefined;
	const reason = (details as { reason?: unknown }).reason;
	return typeof reason === "string" && reason !== "" ? reason : "invalid";
}

/**
 * Whether what follows "@" reads as a path (it has a slash or a dot), so only
 * files are offered: people and teams hide, and `@scope/pkg` never offers a
 * person.
 */
export function isPathQuery(query: string): boolean {
	return /[/.]/.test(query);
}

function nameMatch(name: string, query: string): number | null {
	const needle = query.trim().toLowerCase();
	const haystack = name.toLowerCase();
	if (needle === "") return 0;
	if (haystack.startsWith(needle)) return 1;
	if (haystack.split(/\s+/).some((word) => word.startsWith(needle))) return 2;
	return haystack.includes(needle) ? 3 : null;
}

function byMatch<T>(items: readonly T[], name: (item: T) => string, query: string): T[] {
	return items
		.map((item, index) => ({ item, index, score: nameMatch(name(item), query) }))
		.filter((entry): entry is { item: T; index: number; score: number } => entry.score !== null)
		.sort((a, b) => a.score - b.score || a.index - b.index)
		.map((entry) => entry.item);
}

export interface MentionSections {
	inSession: CloudCpMentionPerson[];
	teams: CloudCpMentionTeam[];
	workspace: CloudCpMentionPerson[];
}

/**
 * The @ menu's people and teams for a query, in the menu's fixed order: the
 * people in the session (those here now first, at most five), the workspace's
 * teams, then the workspace's other people. Only what the control plane
 * returned is offered (it lists only people who can open the session, and a
 * guest gets no teams or workspace people); a path query has none. The
 * control plane filters by `q` too; this keeps the menu right while a newer
 * answer is on its way.
 */
export function mentionSections(
	candidates: { people: readonly CloudCpMentionPerson[]; teams: readonly CloudCpMentionTeam[] } | undefined,
	query: string,
	excludeUserId?: string,
): MentionSections {
	if (!candidates || isPathQuery(query)) return { inSession: [], teams: [], workspace: [] };
	const people = byMatch(candidates.people.filter((person) => person.userId !== excludeUserId), (person) => person.name, query);
	const inSession = people.filter((person) => person.inSession);
	return {
		inSession: [...inSession.filter((person) => person.present), ...inSession.filter((person) => !person.present)].slice(0, IN_SESSION_LIMIT),
		teams: byMatch(candidates.teams, (team) => team.name, query),
		workspace: people.filter((person) => !person.inSession),
	};
}
