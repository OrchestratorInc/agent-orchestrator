import type {
	CloudCpEventMention,
	CloudCpMentionPerson,
	CloudCpMentionTeam,
	CloudCpMentionToken,
} from "./cloud-cp/types";
import type { MessageKey } from "../i18n/messages";
import { CloudCpError } from "./cloud-cp/errors";

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

/**
 * The workspace's name as the @ menu titles its people, or undefined for the
 * neutral title: a personal workspace is named by its owner's email, and a
 * name in the menu is never an email address.
 */
export function mentionWorkspaceName(name: string | undefined): string | undefined {
	const trimmed = name?.trim();
	return trimmed && !/[^\s@]+@[^\s@]+\.[^\s@]+/.test(trimmed) ? trimmed : undefined;
}

/** What a chip reads as in the text: `@` and the name. */
export function mentionText(mention: Pick<ComposerMention, "name">): string {
	return `@${mention.name}`;
}

/**
 * Picks each mention's `@Name` out of text, for drawing it as a chip: where a
 * mention starts the text or follows whitespace and ends where the name does.
 * Longer names win, so "@Chen Wei" is not read as "@Chen". Each `@Name` takes
 * the next mention of that name, in order, so two people who share a name (or
 * a person and a team) stay the ones written there; past the last of a name
 * it takes that name's last mention again, so a list with each person once
 * still draws every repeat. One splitter carries that order across the lines
 * of one text.
 */
export function mentionSplitter<T extends { name: string }>(mentions: readonly T[]): (text: string) => Array<string | T> {
	const queues = new Map<string, T[]>();
	for (const mention of mentions) {
		if (mention.name === "") continue;
		const queue = queues.get(mention.name) ?? [];
		queue.push(mention);
		queues.set(mention.name, queue);
	}
	const names = [...queues.keys()].sort((a, b) => b.length - a.length);
	const last = new Map<string, T>();
	const take = (name: string): T => {
		const next = queues.get(name)!.shift() ?? last.get(name)!;
		last.set(name, next);
		return next;
	};
	return (text) => {
		if (names.length === 0 || !text.includes("@")) return [text];
		const parts: Array<string | T> = [];
		let plain = "";
		let index = 0;
		while (index < text.length) {
			if (text[index] === "@" && (index === 0 || /\s/.test(text[index - 1]!))) {
				const name = names.find((candidate) => text.startsWith(`@${candidate}`, index) &&
					!/[\p{L}\p{N}_]/u.test(text[index + candidate.length + 1] ?? " "));
				if (name !== undefined) {
					if (plain) parts.push(plain);
					plain = "";
					parts.push(take(name));
					index += name.length + 1;
					continue;
				}
			}
			plain += text[index];
			index += 1;
		}
		if (plain) parts.push(plain);
		return parts;
	};
}

/** Text with each mention's `@Name` picked out (mentionSplitter), for one piece of text. */
export function splitByMentions<T extends { name: string }>(text: string, mentions: readonly T[]): Array<string | T> {
	return mentionSplitter(mentions)(text);
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
 * outside it, an unknown id, too many, or teams that reach too many people.
 * Nothing was sent.
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

/** A refused mention's reason (mentionRefusal), as the message that says it in the reader's words. */
export function mentionRefusalKey(reason: string): MessageKey {
	return reason === "too_many" ? "mention.refused.tooMany"
		: reason === "no_access" ? "mention.refused.noAccess"
		: reason === "outside_session" ? "mention.refused.outsideSession"
		: reason === "team_too_large" ? "mention.refused.teamTooLarge"
		: "mention.refused.unknown";
}

/**
 * A write with mentions the control plane paused (429 rate_limited: too many
 * in this session lately), as the seconds until it takes mentions again from
 * its Retry-After (a minute when it names none). Nothing was stored.
 */
export function mentionRateLimit(error: unknown): number | undefined {
	if (!(error instanceof CloudCpError) || error.status !== 429 || error.code !== "rate_limited") return undefined;
	return Math.max(1, Math.ceil((error.retryAfterMs ?? 60_000) / 1000));
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
