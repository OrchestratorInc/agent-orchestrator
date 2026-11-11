import { useCallback, useEffect, useSyncExternalStore } from "react";
import { forgetOnCloudAccountChange } from "./accountCaches";
import type { CloudClient } from "./client";
import { CloudError } from "./errors";
import type { CloudBillingSummary } from "./types";

// The organization's plan, read once for every screen that shows it (Settings,
// New task, the plan sheet) and kept for half a minute, as the desktop does.
// A plan refusal marks it stale, so the numbers shown with it are current.

/** How long a plan read stays current before a screen that shows it reads it again. */
const FRESH_MS = 30_000;

type Entry = {
	orgId: string;
	summary?: CloudBillingSummary;
	error?: unknown;
	fetchedAt?: number;
	stale: boolean;
	pending?: Promise<void>;
};

// By organization, so screens on two organizations during a switch never
// replace each other's read.
const entries = new Map<string, Entry>();
const listeners = new Set<() => void>();

function emit(): void {
	for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

/** What a control plane without plans answers in effect: nothing applies. */
const NO_PLANS: CloudBillingSummary = { enabled: false, billingMode: "off", plan: "", entitled: false };

/**
 * Reads the organization's plan, unless a read for it is under way or still
 * fresh. A result that arrives after the account or organization changed is
 * dropped.
 */
export function loadCloudPlan(client: CloudClient, orgId: string, options: { force?: boolean; now?: number } = {}): Promise<void> {
	const now = options.now ?? Date.now();
	const current = entries.get(orgId);
	if (current?.pending) return current.pending;
	if (!options.force && current?.fetchedAt !== undefined && !current.stale && now - current.fetchedAt < FRESH_MS) return Promise.resolve();
	const next: Entry = { orgId, summary: current?.summary, error: current?.error, fetchedAt: current?.fetchedAt, stale: current?.stale ?? false };
	const pending = client.getBilling(orgId).then(
		(summary) => {
			if (entries.get(orgId) !== next) return;
			entries.set(orgId, { orgId, summary, fetchedAt: Date.now(), stale: false });
			emit();
		},
		(error: unknown) => {
			if (entries.get(orgId) !== next) return;
			// A control plane that predates plans has no such route: nothing to show.
			if (error instanceof CloudError && error.status === 404) entries.set(orgId, { orgId, summary: NO_PLANS, fetchedAt: Date.now(), stale: false });
			// A failed refresh keeps what was already shown.
			else entries.set(orgId, { orgId, summary: next.summary, error, fetchedAt: next.fetchedAt, stale: true });
			emit();
		},
	);
	next.pending = pending;
	entries.set(orgId, next);
	emit();
	return pending;
}

/** The plan just refused something: the next screen that shows it reads it again. */
export function invalidateCloudPlan(): void {
	let changed = false;
	for (const [orgId, entry] of entries) {
		if (entry.stale) continue;
		entries.set(orgId, { ...entry, stale: true });
		changed = true;
	}
	if (changed) emit();
}

export function clearCloudPlan(): void {
	entries.clear();
	emit();
}

forgetOnCloudAccountChange(clearCloudPlan);

export type CloudPlanState = {
	/** loading: nothing read yet; failed: the first read failed. A later failure keeps the last plan. */
	status: "loading" | "ready" | "failed";
	summary?: CloudBillingSummary;
	error?: unknown;
	reload(): Promise<void>;
};

/**
 * The plan of `orgId`, read when the screen shows it and again once it is
 * half a minute old or a refusal made it stale. Undefined without an
 * organization (signed out, still connecting).
 */
export function useCloudPlan(client: CloudClient, orgId: string | undefined): CloudPlanState | undefined {
	const read = useCallback(() => (orgId ? entries.get(orgId) : undefined), [orgId]);
	const own = useSyncExternalStore(subscribe, read, read);
	const needsRead = Boolean(orgId) && (!own || own.stale);
	useEffect(() => {
		if (orgId && needsRead) void loadCloudPlan(client, orgId);
	}, [client, needsRead, orgId]);
	const reload = useCallback(async () => {
		if (orgId) await loadCloudPlan(client, orgId);
	}, [client, orgId]);
	if (!orgId) return undefined;
	if (own?.summary) return { status: "ready", summary: own.summary, ...(own.error ? { error: own.error } : {}), reload };
	if (own?.error && !own.pending) return { status: "failed", error: own.error, reload };
	return { status: "loading", reload };
}
