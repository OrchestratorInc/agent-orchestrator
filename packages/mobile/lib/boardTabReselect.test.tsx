import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { reselectBoardTab, useScrollToTopOnReselect } from "./boardTabReselect";
import type { BoardTabName } from "./board-tabs";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

function Page({ tab, list }: { tab: BoardTabName; list: { scrollToOffset: (params: object) => void } }) {
	useScrollToTopOnReselect(tab, { current: list });
	return null;
}

describe("board tab reselect", () => {
	it("scrolls only the re-tapped tab's list to the top", () => {
		const workers = { scrollToOffset: vi.fn() };
		const projects = { scrollToOffset: vi.fn() };
		act(() => {
			renderer = create(<><Page tab="(workers)" list={workers} /><Page tab="(projects)" list={projects} /></>);
		});
		reselectBoardTab("(projects)");
		expect(projects.scrollToOffset).toHaveBeenCalledWith({ offset: 0, animated: true });
		expect(workers.scrollToOffset).not.toHaveBeenCalled();
	});

	it("does nothing once the page has gone", () => {
		const workers = { scrollToOffset: vi.fn() };
		act(() => { renderer = create(<Page tab="(workers)" list={workers} />); });
		act(() => renderer?.unmount());
		renderer = undefined;
		reselectBoardTab("(workers)");
		expect(workers.scrollToOffset).not.toHaveBeenCalled();
	});
});
