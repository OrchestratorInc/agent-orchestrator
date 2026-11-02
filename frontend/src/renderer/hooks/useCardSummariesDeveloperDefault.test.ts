import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { markCardSummariesDeveloperDefault, useCardSummariesDeveloperDefault } from "./useCardSummariesDeveloperDefault";

const { settingsMock, updateMock } = vi.hoisted(() => ({
	settingsMock: vi.fn(),
	updateMock: vi.fn(),
}));

vi.mock("./useSettings", () => ({
	useSettings: () => ({ settings: settingsMock() }),
	useUpdateCardSummaries: () => ({ update: updateMock }),
}));

describe("useCardSummariesDeveloperDefault", () => {
	beforeEach(() => {
		window.localStorage.clear();
		updateMock.mockReset();
		settingsMock.mockReset().mockReturnValue({ cardSummaries: false });
	});

	it("turns card summaries on once for someone already in Developer mode", () => {
		useUiStore.setState({ developerMode: true });
		const { rerender } = renderHook(() => useCardSummariesDeveloperDefault());
		rerender();
		expect(updateMock).toHaveBeenCalledTimes(1);
		expect(updateMock).toHaveBeenCalledWith(true);
	});

	it("leaves the setting alone outside Developer mode or after the choice was made", () => {
		useUiStore.setState({ developerMode: false });
		renderHook(() => useCardSummariesDeveloperDefault());
		useUiStore.setState({ developerMode: true });
		markCardSummariesDeveloperDefault();
		renderHook(() => useCardSummariesDeveloperDefault());
		expect(updateMock).not.toHaveBeenCalled();
	});
});
