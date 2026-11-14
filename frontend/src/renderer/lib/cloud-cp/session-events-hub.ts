import { subscribeSessionEventsBridged, type CloudCpStreamBridge } from "./stream-bridge";
import type { CloudCpClientEvent, CloudCpSessionLive } from "./types";

const RECONNECT_MIN_MS = 1_000;
const RECONNECT_MAX_MS = 15_000;
const RESUME_WAIT_STEP_MS = 250;
const RESUME_WAIT_MAX_MS = 5_000;

export interface CloudSessionEventsState {
	open: boolean;
	live: CloudCpSessionLive | undefined;
}

export const CLOSED_CLOUD_SESSION_EVENTS: CloudSessionEventsState = Object.freeze({ open: false, live: undefined });

export type CloudSessionEventsResume = number | "loading" | undefined;

export interface CloudSessionEventsSubscriber {
	onEvent?: (event: CloudCpClientEvent) => void;
	live?: boolean;
	resumeAfter?: () => CloudSessionEventsResume;
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
	lastSequence?: number;
	waitingSince?: number;
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
	if (!live && !events) {
		publish(hub, CLOSED_CLOUD_SESSION_EVENTS);
		return;
	}
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
		connection.controller.abort();
		hub.connection = undefined;
		publish(hub, CLOSED_CLOUD_SESSION_EVENTS);
		const healthy = connection.received || (connection.openedAt !== undefined && Date.now() - connection.openedAt >= RECONNECT_MAX_MS);
		hub.delay = healthy ? RECONNECT_MIN_MS : Math.min(hub.delay * 2, RECONNECT_MAX_MS);
		hub.timer = setTimeout(() => connect(hub), hub.delay);
	});
}

function restart(hub: Hub, from?: number): void {
	const previous = hub.connection;
	hub.connection = undefined;
	previous?.controller.abort();
	if (!wantsLive(hub) && hub.state.live) publish(hub, { open: hub.state.open, live: undefined });
	connect(hub, from);
}

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
	let need: number | undefined;
	for (const subscriber of joined) {
		const resume = subscriber.onEvent && hub.subscribers.has(subscriber) ? subscriber.resumeAfter?.() : undefined;
		if (typeof resume === "number") need = Math.min(need ?? resume, resume);
	}
	if (need !== undefined && connection.after !== undefined && need < connection.after) restart(hub, need);
}

function scheduleSettle(hub: Hub): void {
	if (hub.timer !== undefined) return;
	hub.timer = setTimeout(() => {
		hub.timer = undefined;
		settle(hub);
	}, 0);
}

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

export function cloudSessionEventsState(target: CloudSessionEventsTarget): CloudSessionEventsState {
	return hubs.get(hubKey(target))?.state ?? CLOSED_CLOUD_SESSION_EVENTS;
}
