import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useFileAnnotation } from "../../hooks/useFileAnnotation";
import { repositoryAnnotationModel } from "./repositoryAnnotation";

describe("repositoryAnnotationModel", () => {
	// The real hook, because the bug was two keys disagreeing: begin() stores
	// the ../<repository>/ path, and the box asks with its repository-local one.
	it("scrolls a box opened in a sibling repository into view exactly once", () => {
		const { result } = renderHook(() => repositoryAnnotationModel(useFileAnnotation("sess-1"), "11x"));

		act(() => result.current.begin({ path: "src/App.tsx", side: "file", surface: "review" }));
		const [box] = result.current.targets;

		expect(box.path).toBe("src/App.tsx");
		expect(result.current.consumeOpened(box)).toBe(true);
		expect(result.current.consumeOpened(box)).toBe(false);
	});
});
