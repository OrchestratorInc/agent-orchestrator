import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useSyncExternalStore } from "react";
import type { WhatsNewEntry } from "../../shared/whats-new";
import { aoBridge } from "./bridge";
import { captureRendererEvent } from "./telemetry";

export const whatsNewQueryKey = ["whats-new"] as const;

/** Eligible feed items (main filters by date, version and the kill switches), newest first. */
export function useWhatsNew() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: whatsNewQueryKey,
		queryFn: (): Promise<WhatsNewEntry[]> => aoBridge.whatsNew.list(),
		staleTime: Infinity,
		retry: false,
		throwOnError: false,
	});
	useEffect(
		() => aoBridge.whatsNew.onChanged(() => void queryClient.invalidateQueries({ queryKey: whatsNewQueryKey })),
		[queryClient],
	);
	const items = query.data ?? [];
	const markSeen = useCallback(
		(ids: string[]) => {
			if (ids.length === 0) return;
			const seen = new Set(ids);
			queryClient.setQueryData<WhatsNewEntry[]>(whatsNewQueryKey, (current) =>
				current?.map((item) => (seen.has(item.id) ? { ...item, seen: true } : item)),
			);
			void aoBridge.whatsNew.dismiss(ids).catch(() => undefined);
		},
		[queryClient],
	);
	return { items, unseen: items.filter((item) => !item.seen), markSeen };
}

// The panel can be opened from the sidebar card and from Settings; one flag
// shared through a tiny external store keeps a single dialog mounted.
let panelOpen = false;
const panelListeners = new Set<() => void>();
export function setWhatsNewPanelOpen(open: boolean): void {
	if (panelOpen === open) return;
	panelOpen = open;
	for (const listener of panelListeners) listener();
}
export function useWhatsNewPanelOpen(): boolean {
	return useSyncExternalStore(
		(listener) => {
			panelListeners.add(listener);
			return () => panelListeners.delete(listener);
		},
		() => panelOpen,
	);
}

// Impressions count once per item per launch; the card stays until dismissed,
// so a later launch that shows it again is a new impression.
const shownThisLaunch = new Set<string>();
export function reportWhatsNewShown(id: string): void {
	if (shownThisLaunch.has(id)) return;
	shownThisLaunch.add(id);
	void captureRendererEvent("ao.whats_new.shown", { item_id: id });
}
export function reportWhatsNewClicked(id: string): void {
	void captureRendererEvent("ao.whats_new.clicked", { item_id: id });
}
export function reportWhatsNewDismissed(id: string): void {
	void captureRendererEvent("ao.whats_new.dismissed", { item_id: id });
}
