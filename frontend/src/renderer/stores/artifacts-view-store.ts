import { create } from "zustand";
import type { SharedArtifactLink } from "../lib/artifacts";

export type ArtifactViewMode = "gallery" | "list";

/** The order of the Artifacts list; pinned artifacts stay on top in either. */
export type ArtifactSort = "updated" | "name";

type ArtifactViewPreferences = {
	view: ArtifactViewMode;
	sort: ArtifactSort;
	collapsedSections: string[];
	pinnedArtifactIds: string[];
};

const STORAGE_KEY = "ao.artifacts.view";

function readPreferences(): ArtifactViewPreferences {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "null");
		if (saved && typeof saved === "object") {
			return {
				view: "view" in saved && saved.view === "list" ? "list" : "gallery",
				sort: "sort" in saved && saved.sort === "name" ? "name" : "updated",
				collapsedSections: "collapsedSections" in saved && Array.isArray(saved.collapsedSections)
					? saved.collapsedSections.filter((id): id is string => typeof id === "string") : [],
				pinnedArtifactIds: "pinnedArtifactIds" in saved && Array.isArray(saved.pinnedArtifactIds)
					? saved.pinnedArtifactIds.filter((id): id is string => typeof id === "string") : [],
			};
		}
	} catch {}
	return { view: "gallery", sort: "updated", collapsedSections: [], pinnedArtifactIds: [] };
}

function savePreferences(preferences: ArtifactViewPreferences): void {
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(preferences));
	} catch {}
}

type ArtifactsViewState = ArtifactViewPreferences & {
	/**
	 * A shared link opened from outside the app (ao-app://shared-artifact),
	 * which the Artifacts page opens in its preview once, then clears. Never
	 * saved.
	 */
	openSharedArtifact: SharedArtifactLink | null;
	requestOpenSharedArtifact: (artifact: SharedArtifactLink | null) => void;
	setView: (view: ArtifactViewMode) => void;
	setSort: (sort: ArtifactSort) => void;
	toggleSection: (sectionId: string) => void;
	togglePinnedArtifact: (artifactId: string) => void;
	unpinArtifact: (artifactId: string) => void;
};

/** The saved preferences of `state` with `change` applied, written to storage. */
function changed(state: ArtifactViewPreferences, change: Partial<ArtifactViewPreferences>): ArtifactViewPreferences {
	const preferences = {
		view: state.view,
		sort: state.sort,
		collapsedSections: state.collapsedSections,
		pinnedArtifactIds: state.pinnedArtifactIds,
		...change,
	};
	savePreferences(preferences);
	return preferences;
}

export const useArtifactsViewStore = create<ArtifactsViewState>((set) => ({
	...readPreferences(),
	openSharedArtifact: null,
	requestOpenSharedArtifact: (artifact) => set({ openSharedArtifact: artifact }),
	setView: (view) => set((state) => changed(state, { view })),
	setSort: (sort) => set((state) => changed(state, { sort })),
	toggleSection: (sectionId) => set((state) => changed(state, {
		collapsedSections: state.collapsedSections.includes(sectionId)
			? state.collapsedSections.filter((id) => id !== sectionId)
			: [...state.collapsedSections, sectionId],
	})),
	togglePinnedArtifact: (artifactId) => set((state) => changed(state, {
		pinnedArtifactIds: state.pinnedArtifactIds.includes(artifactId)
			? state.pinnedArtifactIds.filter((id) => id !== artifactId)
			: [artifactId, ...state.pinnedArtifactIds],
	})),
	unpinArtifact: (artifactId) => set((state) => changed(state, {
		pinnedArtifactIds: state.pinnedArtifactIds.filter((id) => id !== artifactId),
	})),
}));
