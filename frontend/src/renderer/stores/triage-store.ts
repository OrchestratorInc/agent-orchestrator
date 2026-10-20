import { create } from "zustand";
import type { TriageChoice, TriageOrigin, TriageReplyInput, TriageRun, TriageSelection } from "../../shared/triage";
import { aoBridge } from "../lib/bridge";
import { rememberTaskComposerPreference } from "../lib/task-composer-preferences";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { queryClient } from "../lib/query-client";
import { cachedTriageModels, isTriageSessionRun } from "../lib/triage";

type TriageState = {
	choices: TriageChoice[];
	selection: TriageSelection | null;
	catalogLoaded: boolean;
	loading: boolean;
	starting: boolean;
	preparationId: string | null;
	preparationGeneration: number;
	preparing: boolean;
	preparationPromise: Promise<TriageRun | undefined> | null;
	preparationError: string | null;
	error: string | null;
	runs: TriageRun[];
	runErrors: Record<string, string | undefined>;
	pending: Record<string, boolean | undefined>;
	origin: TriageOrigin;
	setContext: (origin?: TriageOrigin) => void;
	setSelection: (value: TriageSelection) => void;
	loadCatalog: (project?: (native: TriageChoice[]) => TriageChoice[] | undefined) => Promise<void>;
	loadRuns: () => Promise<void>;
	loadRun: (id: string) => Promise<void>;
	receive: (run: TriageRun) => void;
	start: () => Promise<TriageRun | undefined>;
	prepare: (origin: TriageOrigin) => Promise<TriageRun | undefined>;
	discardPreparation: () => void;
	reply: (id: string, text?: string, input?: TriageReplyInput) => Promise<void>;
	cancel: (id: string) => Promise<void>;
};

const message = (error: unknown) => error instanceof Error ? error.message : String(error);

export const useTriageStore = create<TriageState>((set, get) => {
	const runAction = async (id: string, action: () => Promise<TriageRun>, interrupt = false) => {
		if (get().pending[id] && !interrupt) return;
		set((state) => ({ pending: { ...state.pending, [id]: true }, runErrors: { ...state.runErrors, [id]: undefined } }));
		try { get().receive(await action()); }
		catch (error) { set((state) => ({ runErrors: { ...state.runErrors, [id]: message(error) } })); if (!interrupt) throw error; }
		finally { set((state) => ({ pending: { ...state.pending, [id]: false } })); }
	};
	return {
		choices: [], selection: null, catalogLoaded: false, loading: false, starting: false, error: null,
		preparationId: null, preparationGeneration: 0, preparing: false, preparationPromise: null, preparationError: null,
		runs: [], runErrors: {}, pending: {}, origin: {},
		setContext: (origin) => set({ origin: origin ?? {} }),
		setSelection: (selection) => set({ selection }),
		prepare: (origin) => {
			const uncertain = get().runs.find((run) => run.execution === "session" && !run.sessionId);
			if (uncertain) {
				const promise = Promise.resolve(uncertain);
				set({ preparationId: uncertain.id, preparationPromise: promise, preparing: false, preparationError: null });
				return promise;
			}
			get().discardPreparation();
			const generation = get().preparationGeneration;
			set({ preparing: true, preparationError: null });
			const promise = (async () => {
				try {
					const run = await aoBridge.triage.prepare(origin);
					get().receive(run);
					const current = get().runs.find((item) => item.id === run.id) ?? run;
					if (get().preparationGeneration !== generation) {
						if (current.preparation && !isTriageSessionRun(current)) get().receive(await aoBridge.triage.cancel(run.id));
						return;
					}
					set({ preparationId: current.preparation ? run.id : null });
					return current;
				} catch (error) {
					if (get().preparationGeneration === generation) set({ preparationError: message(error) });
				} finally {
					if (get().preparationGeneration === generation) set({ preparing: false });
				}
			})();
			set({ preparationPromise: promise });
			return promise;
		},
		discardPreparation: () => {
			if (get().starting) return;
			const id = get().preparationId;
			const run = get().runs.find((item) => item.id === id);
			if (run && isTriageSessionRun(run) && !run.sessionId) return;
			const preparation = run?.preparation && !isTriageSessionRun(run);
			set((state) => ({ preparationId: null, preparing: false, preparationPromise: null, preparationGeneration: state.preparationGeneration + 1 }));
			if (id && preparation) void aoBridge.triage.cancel(id).then((run) => get().receive(run)).catch(() => {});
		},
		loadCatalog: async (project) => {
			if (get().loading) return;
			set({ loading: true, error: null });
			try {
				const native = await aoBridge.triage.catalog();
				const configured = project?.(native);
				if (configured) await aoBridge.triage.configure(configured);
				const choices = configured ? await aoBridge.triage.catalog() : native;
				set({ choices, catalogLoaded: true });
			} catch (error) { set({ error: message(error), catalogLoaded: true }); }
			finally { set({ loading: false }); }
		},
		loadRuns: async () => {
			try { for (const run of await aoBridge.triage.list()) get().receive(run); }
			catch { /* The launcher shows helper errors. */ }
		},
		loadRun: async (id) => {
			try { get().receive(await aoBridge.triage.get(id)); }
			catch (error) { set((state) => ({ runErrors: { ...state.runErrors, [id]: message(error) } })); }
		},
		receive: (run) => set((state) => {
			const previous = state.runs.find((item) => item.id === run.id);
			if (previous?.sessionId && run.sessionId && previous.sessionId !== run.sessionId) return state;
			const timestamp = (at: string) => at.replace(/(?:\.(\d+))?Z$/, (_, fraction: string | undefined) => `.${(fraction ?? "").padEnd(9, "0")}Z`);
			if (previous && timestamp(previous.updatedAt) > timestamp(run.updatedAt)) {
				if (!previous.sessionId && run.sessionId) return { runs: state.runs.map((item) => item.id === run.id ? { ...previous, execution: "session" as const, sessionId: run.sessionId } : item) };
				return state;
			}
			const current = previous?.sessionId ? { ...run, execution: "session" as const, sessionId: previous.sessionId } : run;
			return { runs: [...state.runs.filter((item) => item.id !== run.id), current].sort((a, b) => b.createdAt.localeCompare(a.createdAt)) };
		}),
		start: async () => {
			const { selection, starting, origin } = get();
			if (!selection || starting) return;
			set({ starting: true, error: null });
			try {
				const prepared = await get().preparationPromise;
				const existing = prepared && (get().runs.find((run) => run.id === prepared.id) ?? prepared);
				if (existing && !existing.preparation && (!isTriageSessionRun(existing) || existing.sessionId)) {
					get().receive(existing);
					set({ preparationId: null, preparationPromise: null });
					return existing;
				}
				const request = existing?.execution === "session" && !existing.sessionId
					? { ...existing.request, preparationId: existing.request.preparationId || existing.id }
					: { ...selection, ...(prepared ? { preparationId: prepared.id } : {}), origin, publishAuthorized: true };
				const run = await aoBridge.triage.start(request);
				get().receive(run);
				const current = get().runs.find((item) => item.id === run.id) ?? run;
				if (current.execution === "session" && !current.sessionId) set({ preparationId: current.id, preparationPromise: Promise.resolve(current) });
				else set({ preparationId: null, preparationPromise: null });
				if (current.sessionId || current.transport) {
					const mode = cachedTriageModels(queryClient, selection.harness)?.selectionMode === "mode";
					rememberTaskComposerPreference(STANDALONE_WORKSPACE_ID, selection.harness, {
						model: mode ? "" : selection.model ?? "", mode: mode ? selection.model ?? "" : "",
						...(selection.effort ? { effort: selection.effort } : {}),
					});
				}
				return current;
			} catch (error) { set({ error: message(error) }); }
			finally { set({ starting: false }); }
		},
		reply: (id, text = "", input) => runAction(id, () => aoBridge.triage.reply(id, text, input)),
		cancel: (id) => runAction(id, () => aoBridge.triage.cancel(id), true),
	};
});
