import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { useAutoSpawnPauseNotice } from "./useAutoSpawnPauseNotice";

const pause = {
	harness: "claude-code",
	errorCode: "UNSUPPORTED_MODEL",
	message: "The selected model is not available.",
	failures: 3,
	pausedAt: "2026-11-10T10:00:00Z",
};

describe("useAutoSpawnPauseNotice", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ globalToasts: [] });
	});

	it("shows each pause once, and a new pause again", () => {
		const projects = [{ id: "mer", name: "Mer", autoSpawnPauses: [pause] }];
		const { rerender } = renderHook(({ list }) => useAutoSpawnPauseNotice(list), { initialProps: { list: projects } });
		expect(useUiStore.getState().globalToasts.map((toast) => toast.title)).toEqual(["Automatic starts paused for Mer"]);
		expect(useUiStore.getState().globalToasts[0].body).toContain("The selected model is not available.");
		expect(useUiStore.getState().globalToasts[0].body).toContain("30-minute cooldown");

		useUiStore.setState({ globalToasts: [] });
		rerender({ list: [{ ...projects[0] }] });
		renderHook(() => useAutoSpawnPauseNotice(projects));
		expect(useUiStore.getState().globalToasts).toHaveLength(0);

		rerender({ list: [{ ...projects[0], autoSpawnPauses: [{ ...pause, pausedAt: "2026-11-11T10:00:00Z" }] }] });
		expect(useUiStore.getState().globalToasts).toHaveLength(1);
	});

	it("shows nothing without pauses", () => {
		renderHook(() => useAutoSpawnPauseNotice([{ id: "mer", name: "Mer" }]));
		expect(useUiStore.getState().globalToasts).toHaveLength(0);
	});
});
