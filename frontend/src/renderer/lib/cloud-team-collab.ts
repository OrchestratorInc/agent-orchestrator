import { cloudInboxUnavailable } from "./cloud-notifications";
import type {
	CloudCpAsidePayload,
	CloudCpClientEvent,
	CloudCpFeedAway,
	CloudCpFeedItem,
	CloudCpMention,
	CloudCpMentionPerson,
	CloudCpMentionTeam,
	CloudCpSuggestion,
} from "./cloud-cp/types";

// Asides, mentions and the team feed ship together on the control plane, so
// one answer says whether a control plane has them. Learned once per control
// plane from the feed (the probe, the feed page itself, or any of these
// routes) and kept for the app run, the way the bell learns about the inbox:
// a missing route (404 without a code, 405, 501) means it has none; any other
// failure (offline, 5xx) says nothing and stays undecided.

export type TeamCollabSupport = "unknown" | "supported" | "unsupported";

/** Whether an error says the control plane predates asides, mentions and the feed. */
export const teamCollabUnavailable = cloudInboxUnavailable;

const support = new Map<string, TeamCollabSupport>();
const listeners = new Set<() => void>();
const supportKey = (baseUrl: string) => baseUrl.replace(/\/+$/, "");

export function teamCollabSupport(baseUrl: string): TeamCollabSupport {
	return support.get(supportKey(baseUrl)) ?? "unknown";
}

export function recordTeamCollabSupport(baseUrl: string, value: Exclude<TeamCollabSupport, "unknown">): void {
	const key = supportKey(baseUrl);
	if (key === "" || support.get(key) === value) return;
	support.set(key, value);
	for (const listener of listeners) listener();
}

export function subscribeTeamCollabSupport(listener: () => void): () => void {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

/** Test seam: forget what was learned about every control plane. */
export function resetTeamCollabSupport(): void {
	support.clear();
	for (const listener of listeners) listener();
}

/**
 * Runs a request to one of these routes and records what its answer says
 * about the control plane, then answers or rethrows as the request did.
 */
export async function probeTeamCollab<T>(baseUrl: string, request: () => Promise<T>): Promise<T> {
	try {
		const answer = await request();
		recordTeamCollabSupport(baseUrl, "supported");
		return answer;
	} catch (error) {
		if (teamCollabUnavailable(error)) recordTeamCollabSupport(baseUrl, "unsupported");
		throw error;
	}
}

// ---------------------------------------------------------------------------
// Asides
// ---------------------------------------------------------------------------

export const ASIDE_EVENT = "chat.aside";

/** The mention tokens an event records, each with the name it had then. */
export function parseMentions(value: unknown): CloudCpMention[] {
	return Array.isArray(value) ? value.flatMap(eventMention) : [];
}

/** A mention token as an event records it, with the name it had then. */
function eventMention(value: unknown): CloudCpMention[] {
	if (!value || typeof value !== "object") return [];
	const mention = value as Record<string, unknown>;
	const name = typeof mention.name === "string" && mention.name !== "" ? { name: mention.name } : {};
	if (mention.kind === "person" && typeof mention.userId === "string") return [{ kind: "person", userId: mention.userId, ...name }];
	if (mention.kind === "team" && typeof mention.teamId === "string") return [{ kind: "team", teamId: mention.teamId, ...name }];
	return [];
}

/**
 * A `chat.aside` event's payload, or undefined for any other event or a
 * malformed one. An aside from before the author's name was recorded has an
 * empty one.
 */
export function asidePayload(event: Pick<CloudCpClientEvent, "type" | "payload">): CloudCpAsidePayload | undefined {
	if (event.type !== ASIDE_EVENT || !event.payload || typeof event.payload !== "object") return undefined;
	const payload = event.payload as Record<string, unknown>;
	if (typeof payload.asideId !== "string" || payload.asideId === "") return undefined;
	if (typeof payload.text !== "string" || payload.text.trim() === "") return undefined;
	return {
		asideId: payload.asideId,
		text: payload.text,
		authorUserId: typeof payload.authorUserId === "string" ? payload.authorUserId : "",
		authorName: typeof payload.authorName === "string" ? payload.authorName.trim() : "",
		mentions: parseMentions(payload.mentions),
	};
}

// ---------------------------------------------------------------------------
// Suggestions
// ---------------------------------------------------------------------------

export const SUGGESTION_CREATED_EVENT = "chat.suggestion_created";
export const SUGGESTION_RESOLVED_EVENT = "chat.suggestion_resolved";

/** A suggestion event's suggestion, or undefined for any other event. */
export function suggestionFromEvent(event: Pick<CloudCpClientEvent, "type" | "payload">): CloudCpSuggestion | undefined {
	if (event.type !== SUGGESTION_CREATED_EVENT && event.type !== SUGGESTION_RESOLVED_EVENT) return undefined;
	const payload = event.payload as Partial<CloudCpSuggestion> | null;
	if (!payload || typeof payload !== "object" || typeof payload.id !== "string" || typeof payload.text !== "string" || typeof payload.status !== "string") return undefined;
	return payload as CloudCpSuggestion;
}

/**
 * The pending suggestions after some session events, oldest first: one made
 * joins the list, one sent or dismissed leaves it. Events for other things
 * change nothing.
 */
export function applySuggestionEvents(pending: readonly CloudCpSuggestion[], events: readonly Pick<CloudCpClientEvent, "type" | "payload">[]): CloudCpSuggestion[] {
	let next = [...pending];
	for (const event of events) {
		const suggestion = suggestionFromEvent(event);
		if (suggestion) next = withSuggestion(next, suggestion);
	}
	return next;
}

/** The pending list with one suggestion as it now stands: in it while pending, out once sent or dismissed. */
export function withSuggestion(pending: readonly CloudCpSuggestion[], suggestion: CloudCpSuggestion): CloudCpSuggestion[] {
	const next = pending.filter((candidate) => candidate.id !== suggestion.id);
	if (suggestion.status === "pending") next.push(suggestion);
	return next.sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
}

/** Whether a send or dismiss failed because the suggestion was already sent or dismissed. */
export function suggestionResolvedError(error: unknown): boolean {
	return typeof error === "object" && error !== null && (error as { status?: unknown }).status === 409 &&
		(error as { code?: unknown }).code === "suggestion_resolved";
}

/** Whether a send or dismiss failed because someone else drives the session. */
export function composerLockedError(error: unknown): boolean {
	return typeof error === "object" && error !== null && (error as { status?: unknown }).status === 409 &&
		(error as { code?: unknown }).code === "COMPOSER_LOCKED";
}

// ---------------------------------------------------------------------------
// Mentions
// ---------------------------------------------------------------------------

/** People shown under "In this session" in the @ menu. */
export const IN_SESSION_LIMIT = 5;
/** At most this many people and teams in one message, steer or aside (the contract's cap). */
export const MAX_MENTIONS = 20;
/** A team mention above this many people asks before it notifies them. */
export const TEAM_CONFIRM_THRESHOLD = 5;

/**
 * A mention chip in the composer: who it names and what the menu knew about
 * them when it was picked, so a send can ask first without looking them up
 * again.
 */
export type ComposerMention =
	| { kind: "person"; id: string; name: string; canOpen?: boolean }
	| { kind: "team"; id: string; name: string; memberCount?: number; membersWithoutAccess?: number; namesWithoutAccess?: string[] };

/** The token a chip sends beside the text. */
export function mentionToken(mention: ComposerMention): CloudCpMention {
	return mention.kind === "person" ? { kind: "person", userId: mention.id } : { kind: "team", teamId: mention.id };
}

/** Each person or team once, in the order they were first mentioned. */
export function uniqueMentions(mentions: readonly ComposerMention[]): ComposerMention[] {
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

/** A guest named someone outside the session (422 mention_not_allowed). */
export function mentionNotAllowedError(error: unknown): boolean {
	return typeof error === "object" && error !== null && (error as { code?: unknown }).code === "mention_not_allowed";
}

/** A mention would notify more people than one send may (422 too_many_mentioned). */
export function tooManyMentionedError(error: unknown): boolean {
	return typeof error === "object" && error !== null && (error as { code?: unknown }).code === "too_many_mentioned";
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
 * people in the session (at most five), teams, then everyone else in the
 * workspace. A path query has none. The control plane filters by `q` too;
 * this keeps the menu right while a newer answer is on its way.
 */
export function mentionSections(
	candidates: { people: readonly CloudCpMentionPerson[]; teams: readonly CloudCpMentionTeam[] } | undefined,
	query: string,
	excludeUserId?: string,
): MentionSections {
	if (!candidates || isPathQuery(query)) return { inSession: [], teams: [], workspace: [] };
	const people = byMatch(candidates.people.filter((person) => person.userId !== excludeUserId), (person) => person.name, query);
	return {
		inSession: people.filter((person) => person.inSession).slice(0, IN_SESSION_LIMIT),
		teams: byMatch(candidates.teams, (team) => team.name, query),
		workspace: people.filter((person) => !person.inSession),
	};
}

/** The teams in a send that name more people than a mention notifies without asking. */
export function teamsToConfirm(mentions: readonly ComposerMention[]): Extract<ComposerMention, { kind: "team" }>[] {
	return mentions.filter((mention): mention is Extract<ComposerMention, { kind: "team" }> =>
		mention.kind === "team" && (mention.memberCount ?? 0) > TEAM_CONFIRM_THRESHOLD);
}

/** The people in a send who can't open the session. */
export function peopleWithoutAccess(mentions: readonly ComposerMention[]): Extract<ComposerMention, { kind: "person" }>[] {
	return mentions.filter((mention): mention is Extract<ComposerMention, { kind: "person" }> =>
		mention.kind === "person" && mention.canOpen === false);
}

// ---------------------------------------------------------------------------
// Team feed
// ---------------------------------------------------------------------------

export type FeedGroupId = "needs" | "working" | "recent";

export interface FeedGroup {
	id: FeedGroupId;
	items: CloudCpFeedItem[];
}

/**
 * The feed's groups in their fixed order: sessions that need someone, ones
 * whose agent is working now, then everything else. Each keeps the server's
 * order (newest activity first); an empty group is left out. A session that
 * shows on two pages (it moved while paging) shows once.
 */
export function groupFeed(items: readonly CloudCpFeedItem[]): FeedGroup[] {
	const seen = new Set<string>();
	const groups: Record<FeedGroupId, CloudCpFeedItem[]> = { needs: [], working: [], recent: [] };
	for (const item of items) {
		const key = `${item.orgId}:${item.sessionId}`;
		if (seen.has(key)) continue;
		seen.add(key);
		groups[item.status === "needs" ? "needs" : item.status === "working" ? "working" : "recent"].push(item);
	}
	return (["needs", "working", "recent"] as const)
		.filter((id) => groups[id].length > 0)
		.map((id) => ({ id, items: groups[id] }));
}

/** Whether "While you were away" has anything to say. */
export function awayHasNews(away: CloudCpFeedAway | undefined): boolean {
	if (!away) return false;
	return away.needsYou > 0 || away.prsMerged.length > 0 || away.failed.length > 0 || away.started.count > 0;
}

/** One question before a mentioning send: a big team first, then the people who can't open the session. */
export type MentionAskStep =
	| { kind: "team"; team: Extract<ComposerMention, { kind: "team" }> }
	| { kind: "noAccess"; people: Extract<ComposerMention, { kind: "person" }>[] };

export function mentionAskSteps(mentions: readonly ComposerMention[]): MentionAskStep[] {
	const outside = peopleWithoutAccess(mentions);
	return [
		...teamsToConfirm(mentions).map((team): MentionAskStep => ({ kind: "team", team })),
		...(outside.length > 0 ? [{ kind: "noAccess" as const, people: outside }] : []),
	];
}
