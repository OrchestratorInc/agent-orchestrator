// How current one machine's rows are, as the dot beside its name says it.
// Free of React Native imports so it can be unit-tested directly.
//
// This replaced a full-width banner above the board. The banner spoke for one
// machine without naming it, while the board mixed that machine's rows with
// Cloud's and other machines'. It also keyed its red state on the poll's error,
// which a failed endpoint race never sets, so a machine that had stopped
// answering read as an amber "Showing data from 3m ago" instead.
//
// The state belongs to the machine, so it is drawn on the machine: a dot on its
// filter chip and beside its name on each row. Healthy machines draw nothing.

import { classifyLink, EMPTY_LINK_TRACKER, type LinkTracker, type PhoneNetwork } from "./linkHealth";
import { STALE_AFTER_MS, staleAgeLabel } from "./screenState";
import type { Theme } from "./theme";

/**
 * - live: current, or still making its first connection. Draws nothing.
 * - lagging: its rows have not been refreshed for a while, or it has just
 *   stopped answering. Amber.
 * - unreachable: it has stopped answering for long enough to call it down, or
 *   it answers with an error (a rejected password, an incompatible version).
 *   Red.
 */
export type MachineFreshness = "live" | "lagging" | "unreachable";

export type FreshnessInput = {
	connection: "closed" | "connecting" | "open";
	loading: boolean;
	error: string | null;
	lastSyncAt: number;
	linkTracker?: LinkTracker;
};

/**
 * The phone having no network is not the machine's fault, so it never lights a
 * machine's dot: every machine would turn red at once and the cause would be
 * nowhere on screen. The page says it once instead (see `phoneOffline`).
 */
export function machineFreshness(host: FreshnessInput, phone: PhoneNetwork, now: number): MachineFreshness {
	const link = classifyLink(host.linkTracker ?? EMPTY_LINK_TRACKER, phone, now);
	if (link === "phone-offline") return "live";
	if (link === "unreachable") return "unreachable";
	// Answered, but with an error: the link works and the machine said no.
	if (host.connection !== "open" && host.error && !host.loading) return "unreachable";
	if (link === "reconnecting") return "lagging";
	// Never synced is a first connection, which the board's loader covers.
	if (host.lastSyncAt > 0 && now - host.lastSyncAt >= STALE_AFTER_MS) return "lagging";
	return "live";
}

/** Freshness per machine id, for the chips and rows to look up. */
export function freshnessByHost(
	hosts: readonly (FreshnessInput & { hostId: string })[],
	phone: PhoneNetwork,
	now: number,
): Map<string, MachineFreshness> {
	return new Map(hosts.map((host) => [host.hostId, machineFreshness(host, phone, now)]));
}

/** A string that changes only when some machine's freshness does, for memo keys. */
export function freshnessKey(freshness: ReadonlyMap<string, MachineFreshness>): string {
	let key = "";
	for (const [id, state] of freshness) if (state !== "live") key += `${id}=${state}\n`;
	return key;
}

export type FreshnessTone = "muted" | "warning" | "danger";

/** The dot's tone for a machine, or null for a live one, which wears no dot. */
export function freshnessTone(freshness: MachineFreshness | undefined): FreshnessTone | null {
	return freshness === "unreachable" ? "danger" : freshness === "lagging" ? "warning" : null;
}

export function freshnessColor(t: Pick<Theme, "red" | "amber" | "textTertiary">, tone: FreshnessTone): string {
	return tone === "danger" ? t.red : tone === "warning" ? t.amber : t.textTertiary;
}

/** Spoken after the machine's name, so a screen reader hears what the dot shows. */
export function freshnessAccessibilityLabel(freshness: MachineFreshness | undefined): string {
	switch (freshness) {
		case "lagging":
			return "updates delayed";
		case "unreachable":
			return "can't reach";
		default:
			return "";
	}
}

/**
 * The one line under the page title, or null when the page has nothing to say.
 *
 * Only for a page whose rows are all on one machine, where a dot on every row
 * would repeat itself, and for the phone being offline, which affects every
 * machine at once. A page showing several machines leaves it to their dots.
 */
export function freshnessLine({
	machine,
	freshness,
	lastSyncAt,
	phoneOffline,
	now,
}: {
	machine: string | undefined;
	freshness: MachineFreshness | undefined;
	lastSyncAt: number;
	phoneOffline: boolean;
	now: number;
}): { tone: FreshnessTone; text: string; retry: boolean } | null {
	const age = lastSyncAt > 0 ? ` · updated ${staleAgeLabel(now - lastSyncAt)}` : "";
	if (phoneOffline) return { tone: "muted", text: `You're offline${age}`, retry: false };
	const name = machine?.trim() || "your machine";
	if (freshness === "unreachable") return { tone: "danger", text: `Can't reach ${name}${age}`, retry: true };
	if (freshness !== "lagging") return null;
	// Lagging only because the link just dropped: the rows are still recent, so
	// their age says nothing useful yet.
	const recent = lastSyncAt <= 0 || now - lastSyncAt < STALE_AFTER_MS;
	return { tone: "warning", text: recent ? `Reconnecting to ${name}…` : `${name}${age}`, retry: true };
}
