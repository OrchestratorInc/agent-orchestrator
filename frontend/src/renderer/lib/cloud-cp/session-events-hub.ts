// One `/sessions/{id}/events` stream per Cloud session, shared by everything
// on screen that follows the session: its chat, its live state (who has it
// open, who holds the composer), its pull requests, its workspace review, its
// Setup log and its startup loader. Each used to open its own stream, so one
// open session held three or more, out of the eight a window may have. The
// stream opens with the first subscriber and closes with the last.

import { subscribeSessionEventsBridged, type CloudCpStreamBridge } from "./stream-bridge";
import type { CloudCpClientEvent, CloudCpSessionLive } from "./types";

const RECONNECT_MIN_MS = 1_000;
const RECONNECT_MAX_MS = 15_000;
/** How often a connect looks again for a subscriber still loading what it holds. */
const RESUME_WAIT_STEP_MS = 250;
/** How long a connect waits for such a subscriber before it starts without it. */
const RESUME_WAIT_MAX_MS = 5_000;

/** Whether the session's stream is up, and its latest live state (live streams only). */
export interface CloudSessionEventsState {
	open: boolean;
	live: CloudCpSessionLive | undefined;
}

export const CLOSED_CLOUD_SESSION_EVENTS: CloudSessionEventsState = Object.freeze({ open: false, live: undefined });

/**
 * Where a subscriber's events start: the newest sequence it already holds, so
 * the stream replays nothing older; "loading" while it is still reading what
 * it holds (the stream waits a little for it); undefined when it takes
 * whatever the stream sends.
 */
export type CloudSessionEventsResume = number | "loading" | undefined;

export interface CloudSessionEventsSubscriber {
	/** Each client event, in stream order. It may repeat one already held. */
	onEvent?: (event: CloudCpClientEvent) => void;
	/** Lists this person as a viewer of the session while subscribed, and fills the state's `live`. */
	live?: boolean;
	/** Read when the stream (re)connects with nothing to resume from yet. */
	resumeAfter?: () => CloudSessionEventsResume;
	/** The stream opened, dropped or sent new live state. */
	onStateChange?: (state: CloudSessionEventsState) => void;
}

export interface CloudSessionEventsTarget {
	baseUrl: string;
	orgId: string;
	sessionId: string;
}

interface Connection {
	controller: AbortController;
	live: boolean;
	events: boolean;
	after: number | undefined;
	openedAt?: number;
	received: boolean;
}

interface Hub {
	key: string;
	target: CloudSessionEventsTarget;
	bridge?: CloudCpStreamBridge;
	subscribers: Set<CloudSessionEventsSubscriber>;
	state: CloudSessionEventsState;
	connection?: Connection;
	timer?: ReturnType<typeof setTimeout>;
	delay: number;
	/** The newest sequence the stream delivered: a reconnect resumes after it. */
	lastSequence?: number;
	waitingSince?: number;
	/** Subscribers that joined since the connection was last brought in line. */
	joined: CloudSessionEventsSubscriber[];
}

const hubs = new Map<string, Hub>();

function hubKey(target: CloudSessionEventsTarget): string {
	return `${target.baseUrl.replace(/\/+$/, "")}|${target.orgId}|${target.sessionId}`;
}

function wantsLive(hub: Hub): boolean {
	for (const subscriber of hub.subscribers) if (subscriber.live) return true;
	return false;
}

function wantsEvents(hub: Hub): boolean {
	for (const subscriber of hub.subscribers) if (subscriber.onEvent) return true;
	return false;
}

function publish(hub: Hub, state: CloudSessionEventsState): void {
	if (hub.state.open === state.open && hub.state.live === state.live) return;
	hub.state = state.open || state.live ? state : CLOSED_CLOUD_SESSION_EVENTS;
	for (const subscriber of [...hub.subscribers]) subscriber.onStateChange?.(hub.state);
}

function clearTimer(hub: Hub): void {
	if (hub.timer !== undefined) clearTimeout(hub.timer);
	hub.timer = undefined;
}

/** Where a new connection starts: after the newest delivered event, else after what every subscriber holds. */
function resumePoint(hub: Hub, from: number | undefined): { after: number | undefined } | "wait" {
	if (from !== undefined) return { after: from };
	if (hub.lastSequence !== undefined) return { after: hub.lastSequence };
	const known: number[] = [];
	let loading = false;
	for (const subscriber of hub.subscribers) {
		if (!subscriber.onEvent) continue;
		const resume = subscriber.resumeAfter?.();
		if (resume === "loading") loading = true;
		else if (typeof resume === "number") known.push(resume);
	}
	if (loading) {
		hub.waitingSince ??= Date.now();
		if (Date.now() - hub.waitingSince < RESUME_WAIT_MAX_MS) return "wait";
	}
	return { after: known.length ? Math.min(...known) : undefined };
}

function connect(hub: Hub, from?: number): void {
	clearTimer(hub);
	hub.joined = [];
	const live = wantsLive(hub);
	const events = wantsEvents(hub);
	if (!live && !events) return;
	const resume = events ? resumePoint(hub, from) : { after: undefined };
	if (resume === "wait") {
		hub.timer = setTimeout(() => connect(hub), RESUME_WAIT_STEP_MS);
		return;
	}
	hub.waitingSince = undefined;
	const connection: Connection = { controller: new AbortController(), live, events, after: resume.after, received: false };
	hub.connection = connection;
	const { baseUrl, orgId, sessionId } = hub.target;
	void subscribeSessionEventsBridged({
		baseUrl,
		orgId,
		sessionId,
		after: resume.after,
		live,
		// A stream only for live state asks for no client events, so nothing is replayed.
		types: events ? undefined : ["session.live"],
		signal: connection.controller.signal,
		bridge: hub.bridge,
		onOpen: () => {
			connection.openedAt = Date.now();
			publish(hub, { open: true, live: hub.state.live });
		},
		onLive: (state) => {
			connection.received = true;
			publish(hub, { open: true, live: state });
		},
		onEvent: (event) => {
			connection.received = true;
			if (typeof event.sequence === "number") {
				hub.lastSequence = Math.max(hub.lastSequence ?? 0, event.sequence);
			}
			for (const subscriber of [...hub.subscribers]) subscriber.onEvent?.(event);
		},
	}).then(() => {
		if (connection.controller.signal.aborted || hub.connection !== connection) return;
		// The ended stream's signal is spent; nothing it started outlives it.
		connection.controller.abort();
		hub.connection = undefined;
		publish(hub, CLOSED_CLOUD_SESSION_EVENTS);
		// A stream that delivered something, or stayed open a while, was
		// healthy: reconnect soon. One refused or cut at once backs off.
		const healthy = connection.received || (connection.openedAt !== undefined && Date.now() - connection.openedAt >= RECONNECT_MAX_MS);
		hub.delay = healthy ? RECONNECT_MIN_MS : Math.min(hub.delay * 2, RECONNECT_MAX_MS);
		hub.timer = setTimeout(() => connect(hub), hub.delay);
	});
}

/** Closes the open connection and opens one that fits the current subscribers. */
function restart(hub: Hub, from?: number): void {
	const previous = hub.connection;
	hub.connection = undefined;
	previous?.controller.abort();
	if (!wantsLive(hub) && hub.state.live) publish(hub, { open: hub.state.open, live: undefined });
	connect(hub, from);
}

/** Brings the connection in line with the current subscribers. */
function settle(hub: Hub): void {
	const connection = hub.connection;
	const joined = hub.joined;
	hub.joined = [];
	if (!connection) {
		connect(hub);
		return;
	}
	if (connection.live !== wantsLive(hub) || connection.events !== wantsEvents(hub)) {
		restart(hub);
		return;
	}
	// One that needs events older than the open stream started from restarts it from there.
	let need: number | undefined;
	for (const subscriber of joined) {
		const resume = subscriber.onEvent && hub.subscribers.has(subscriber) ? subscriber.resumeAfter?.() : undefined;
		if (typeof resume === "number") need = Math.min(need ?? resume, resume);
	}
	if (need !== undefined && connection.after !== undefined && need < connection.after) restart(hub, need);
}

/**
 * Settles the connection once the components mounting or unmounting together
 * have all joined or left, so a session view that mounts its chat, header and
 * inspector at once opens one stream rather than one and then another. A
 * pending reconnect or resume wait settles with the subscribers it finds.
 */
function scheduleSettle(hub: Hub): void {
	if (hub.timer !== undefined) return;
	hub.timer = setTimeout(() => {
		hub.timer = undefined;
		settle(hub);
	}, 0);
}

/**
 * Follows a Cloud session's event stream, sharing one connection with every
 * other subscriber to the same session. It resumes after the newest event it
 * delivered when it reconnects, and reconnects with backoff when it drops.
 * Returns the unsubscribe; the last one out closes the stream.
 */
export function subscribeCloudSessionEvents(
	target: CloudSessionEventsTarget,
	subscriber: CloudSessionEventsSubscriber,
	options: { bridge?: CloudCpStreamBridge } = {},
): () => void {
	const key = hubKey(target);
	let hub = hubs.get(key);
	if (!hub) {
		hub = { key, target, bridge: options.bridge, subscribers: new Set(), state: CLOSED_CLOUD_SESSION_EVENTS, delay: RECONNECT_MIN_MS, joined: [] };
		hubs.set(key, hub);
	}
	const current = hub;
	current.subscribers.add(subscriber);
	current.joined.push(subscriber);
	scheduleSettle(current);
	let subscribed = true;
	return () => {
		if (!subscribed) return;
		subscribed = false;
		current.subscribers.delete(subscriber);
		if (current.subscribers.size > 0) {
			scheduleSettle(current);
			return;
		}
		clearTimer(current);
		current.connection?.controller.abort();
		current.connection = undefined;
		if (hubs.get(key) === current) hubs.delete(key);
	};
}

/** The session stream's current state, for a subscriber that joins an open stream. */
export function cloudSessionEventsState(target: CloudSessionEventsTarget): CloudSessionEventsState {
	return hubs.get(hubKey(target))?.state ?? CLOSED_CLOUD_SESSION_EVENTS;
}
