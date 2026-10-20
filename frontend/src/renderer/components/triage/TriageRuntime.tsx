import { useEffect } from "react";
import { aoBridge } from "../../lib/bridge";
import { configuredTriageSnapshot } from "../../lib/triage";
import { queryClient } from "../../lib/query-client";
import type { TriageChoice } from "../../../shared/triage";
import { useTriageStore } from "../../stores/triage-store";

/** Keeps native fallback runs recoverable when the launcher or daemon route changes. */
export function TriageRuntime() {
	useEffect(() => {
		void useTriageStore.getState().loadRuns();
		const unsubscribeRuns = aoBridge.triage.subscribe((run) => useTriageStore.getState().receive(run));
		let active = true;
		let writes = Promise.resolve();
		let native: TriageChoice[] | undefined;
		let saved = "";
		const sync = () => {
			writes = writes.then(async () => {
				if (!active || !queryClient.getQueryData(["settings"]) || !queryClient.getQueryData(["agent-readiness"])) return;
				const catalog = native ??= await aoBridge.triage.catalog();
				if (!active) return;
				const choices = configuredTriageSnapshot(queryClient, catalog);
				if (!choices) return;
				const fingerprint = JSON.stringify(choices);
				if (fingerprint === saved) return;
				await aoBridge.triage.configure(choices);
				saved = fingerprint;
			}).catch(() => { native = undefined; });
		};
		const unsubscribeCache = queryClient.getQueryCache().subscribe((event) => {
			if (event.type !== "updated" || event.action.type !== "success") return;
			const key = event.query.queryKey;
			if ((key.length === 1 && ["settings", "agent-readiness", "agent-accounts"].includes(String(key[0]))) ||
				(key[0] === "agent-models" && (key.length === 3 || (key.length === 4 && key[3] === "catalog")))) void sync();
		});
		void sync();
		return () => { active = false; unsubscribeRuns(); unsubscribeCache(); };
	}, []);
	return null;
}
