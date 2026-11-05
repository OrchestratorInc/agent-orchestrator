import { create } from "zustand";

/** A model row starred in the agent picker, with its settings. For Cursor, `model` is the exact variant. */
export interface ModelFavorite {
	harness: string;
	/** The row in the agent's model list. */
	row: string;
	/** The model id to select. */
	model: string;
	label: string;
	effort?: string;
	effortLabel?: string;
	fast?: boolean;
	thinking?: boolean;
}

const STORAGE_KEY = "ao.chat.modelFavorites";

function readStored(): ModelFavorite[] {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "[]");
		if (Array.isArray(saved)) {
			return saved.filter((entry) => [entry?.harness, entry?.row, entry?.model, entry?.label].every((field) => typeof field === "string"));
		}
	} catch {}
	return [];
}

const useStore = create<{ favorites: ModelFavorite[] }>(() => ({ favorites: readStored() }));

/** Adds the model to the favourites, or removes it if it is already one. */
export function toggleModelFavorite(favorite: ModelFavorite) {
	const current = useStore.getState().favorites;
	const kept = current.filter((entry) => entry.harness !== favorite.harness || entry.row !== favorite.row);
	const favorites = kept.length === current.length ? [...current, favorite] : kept;
	useStore.setState({ favorites });
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(favorites));
	} catch {
		// A full or blocked store only loses the favourite across restarts.
	}
}

/** The starred models, in the order they were starred. */
export function useModelFavorites(): ModelFavorite[] {
	return useStore((state) => state.favorites);
}
