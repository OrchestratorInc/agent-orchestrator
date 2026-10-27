import { create } from "zustand";

export type ArtifactViewMode = "gallery" | "list";

type ArtifactViewPreferences = {
	view: ArtifactViewMode;
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
				collapsedSections: "collapsedSections" in saved && Array.isArray(saved.collapsedSections)
					? saved.collapsedSections.filter((id): id is string => typeof id === "string") : [],
				pinnedArtifactIds: "pinnedArtifactIds" in saved && Array.isArray(saved.pinnedArtifactIds)
					? saved.pinnedArtifactIds.filter((id): id is string => typeof id === "string") : [],
			};
		}
	} catch {}
	return { view: "gallery", collapsedSections: [], pinnedArtifactIds: [] };
}

function savePreferences(preferences: ArtifactViewPreferences): void {
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(preferences));
	} catch {}
}

type ArtifactsViewState = ArtifactViewPreferences & {
	setView: (view: ArtifactViewMode) => void;
	toggleSection: (sectionId: string) => void;
	togglePinnedArtifact: (artifactId: string) => void;
	unpinArtifact: (artifactId: string) => void;
};

export const useArtifactsViewStore = create<ArtifactsViewState>((set) => ({
	...readPreferences(),
	setView: (view) => set((state) => {
		const preferences = { view, collapsedSections: state.collapsedSections, pinnedArtifactIds: state.pinnedArtifactIds };
		savePreferences(preferences);
		return preferences;
	}),
	toggleSection: (sectionId) => set((state) => {
		const preferences = {
			view: state.view,
			pinnedArtifactIds: state.pinnedArtifactIds,
			collapsedSections: state.collapsedSections.includes(sectionId)
				? state.collapsedSections.filter((id) => id !== sectionId)
				: [...state.collapsedSections, sectionId],
		};
		savePreferences(preferences);
		return preferences;
	}),
	togglePinnedArtifact: (artifactId) => set((state) => {
		const preferences = {
			view: state.view,
			collapsedSections: state.collapsedSections,
			pinnedArtifactIds: state.pinnedArtifactIds.includes(artifactId)
				? state.pinnedArtifactIds.filter((id) => id !== artifactId)
				: [artifactId, ...state.pinnedArtifactIds],
		};
		savePreferences(preferences);
		return preferences;
	}),
	unpinArtifact: (artifactId) => set((state) => {
		const preferences = {
			view: state.view,
			collapsedSections: state.collapsedSections,
			pinnedArtifactIds: state.pinnedArtifactIds.filter((id) => id !== artifactId),
		};
		savePreferences(preferences);
		return preferences;
	}),
}));
