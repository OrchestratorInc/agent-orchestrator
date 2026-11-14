import { SIDE_CLOSE_PROTOCOL_VERSION, type SideCloseEntry, type SideCloseRequest, type SideCloseState } from "../../shared/side-close";

/** Explicit browser-preview transport; desktop IPC errors never enter this path. */
export function createWebSideCloseBridge() {
	const entries = new Map<string, SideCloseEntry>();
	const active = new Map<string, Promise<SideCloseState>>();
	const requests = new Map<string, string>();
	const uncertain = new Set<string>();
	const listeners = new Set<(state: SideCloseState) => void>();
	let epoch = "web:0";
	let base: string | undefined;
	let revision = 0;
	const key = (session: string, side: string) => JSON.stringify([session, side]);
	const summary = (sessionId: string | null): SideCloseState => ({ protocolVersion: SIDE_CLOSE_PROTOCOL_VERSION, epoch, revision, recovery: "ready", sessionId, sides: [...entries.values()].filter(side => !sessionId || side.sessionId === sessionId).map(side => ({ ...side })) });
	const publish = () => { revision++; for (const listener of listeners) { try { listener(summary(null)); } catch { /* another subscriber cannot revoke proof */ } } };
	const transport = async () => {
		const client = await import("./api-client");
		const next = client.getApiBaseUrl();
		if (base !== undefined && next !== base) {
			epoch = `web:${revision + 1}`;
			for (const [id, entry] of entries) { entry.status = "unconfirmed"; entry.version++; uncertain.add(id); }
			publish();
		}
		base = next;
		return client;
	};
	return {
		protocolVersion: SIDE_CLOSE_PROTOCOL_VERSION,
		mode: "web" as "desktop" | "web",
		capture: async () => undefined,
		getCloseState: async (sessionId: string) => { await transport(); return summary(sessionId); },
		onCloseStateChanged: (listener: (state: SideCloseState) => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
		requestClose: async (input: SideCloseRequest): Promise<SideCloseState> => {
			if (!input || [input.sessionId, input.sideId, input.requestId, input.epoch].some(value => typeof value !== "string" || !value || value.length > 255) || !Number.isSafeInteger(input.expectedVersion) || input.expectedVersion < 0) throw new Error("Invalid side close request");
			const { apiClient, getApiBaseUrl } = await transport();
			const id = key(input.sessionId, input.sideId);
			if (requests.has(input.requestId) && requests.get(input.requestId) !== id) throw new Error("Side close request identity was reused");
			if (active.has(id)) return active.get(id)!;
			const old = entries.get(id);
			if (input.epoch !== epoch || old?.status === "closed" || requests.has(input.requestId) || input.expectedVersion !== (old?.version ?? 0)) return summary(input.sessionId);
			requests.set(input.requestId, id);
			const priorUncertain = uncertain.has(id);
			const entry: SideCloseEntry = { id: input.sideId, sessionId: input.sessionId, label: old?.label ?? "", version: (old?.version ?? 0) + 1, status: "closing", operationId: input.requestId };
			entries.set(id, entry);
			publish();
			const controller = new AbortController();
			const requestEpoch = epoch;
			const requestBase = base;
			const same = () => requestEpoch === epoch && getApiBaseUrl() === requestBase;
			const current = () => same() && entries.get(id) === entry;
			let expired = false;
			let expire!: () => void;
			const timeout = new Promise<void>(resolve => { expire = resolve; });
			const timer = setTimeout(() => { expired = true; controller.abort(); expire(); }, 30_000);
			const execute = async () => {
				try {
					uncertain.add(id);
					const { response, error } = await apiClient.DELETE("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", { params: { path: { sessionId: input.sessionId, sideId: input.sideId } }, signal: controller.signal });
					if (!same()) return;
					if (!error && response.status === 204) {
						const latest = entries.get(id)!;
						latest.status = "closed"; latest.version++; uncertain.delete(id); publish();
						return;
					}
					if (!current() || expired || entry.status === "closed") return;
					const code = error && typeof error === "object" && "code" in error && typeof error.code === "string" ? error.code : undefined;
					if (code && !priorUncertain && [400, 401, 403, 405, 409, 422].includes(response.status)) {
						const { data, error: listError } = await apiClient.GET("/api/v1/sessions/{sessionId}/conversation/side-chats", { params: { path: { sessionId: input.sessionId } }, signal: controller.signal });
						if (current() && !expired && entries.get(id)?.status !== "closed" && !listError && data?.sides?.some(side => side.id === input.sideId && side.sessionId === input.sessionId)) { entry.status = "rejected_open"; uncertain.delete(id); return; }
					}
					entry.status = "unconfirmed";
				} catch { if (current() && entry.status !== "closed") entry.status = "unconfirmed"; }
			};
			const promise = Promise.race([Promise.resolve().then(execute), timeout]).then(() => {
				clearTimeout(timer);
				if (current()) { entry.version++; if (entry.status === "closing") entry.status = "unconfirmed"; }
				active.delete(id); publish(); return summary(input.sessionId);
			});
			active.set(id, promise);
			return promise;
		},
	};
}
