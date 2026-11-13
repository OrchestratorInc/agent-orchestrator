import { beforeEach, expect, it } from "vitest";
import { artifactKey } from "../lib/artifacts";
import { useArtifactsViewStore } from "./artifacts-view-store";

beforeEach(() => {
	window.localStorage.removeItem("ao.artifacts.view");
	useArtifactsViewStore.setState({ view: "gallery", collapsedSections: [], pinnedArtifactIds: [] });
});

it("unpins idempotently after a pin changes during deletion", () => {
	const id = artifactKey({ sessionId: "ao-1", path: "report.md" });
	const store = useArtifactsViewStore.getState();
	store.togglePinnedArtifact(id);
	store.togglePinnedArtifact(id);
	store.unpinArtifact(id);
	expect(useArtifactsViewStore.getState().pinnedArtifactIds).toEqual([]);
	expect(JSON.parse(window.localStorage.getItem("ao.artifacts.view") ?? "{}").pinnedArtifactIds).toEqual([]);
});

it("removes only the deleted artifact's pin and preserves view preferences", () => {
	const deleted = artifactKey({ sessionId: "ao-1", path: "report.md" });
	const kept = artifactKey({ sessionId: "ao-2", path: "report.md" });
	const store = useArtifactsViewStore.getState();
	store.setView("list");
	store.toggleSection("project:ao");
	store.togglePinnedArtifact(deleted);
	store.togglePinnedArtifact(kept);
	store.unpinArtifact(deleted);
	expect(useArtifactsViewStore.getState()).toMatchObject({
		view: "list",
		collapsedSections: ["project:ao"],
		pinnedArtifactIds: [kept],
	});
});
