import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SHEET_DISMISS_MS, useLeaveSheetThen } from "./sheetHandoff";

const env = vi.hoisted(() => ({ os: "ios" }));
vi.mock("react-native", () => ({ Platform: { get OS() { return env.os; } } }));

let renderer: ReactTestRenderer | undefined;
let leaveFor: ((open: () => void) => void) | undefined;
const leave = vi.fn();

function Sheet() {
	leaveFor = useLeaveSheetThen(leave);
	return null;
}

function mountSheet() {
	act(() => { renderer = create(<Sheet />); });
}

/** The navigator removes the sheet's route: its screen unmounts. */
function sheetGone() {
	act(() => renderer?.unmount());
	renderer = undefined;
}

beforeEach(() => {
	vi.useFakeTimers();
	env.os = "ios";
	leave.mockReset();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
});

describe("useLeaveSheetThen", () => {
	it("on iOS closes the sheet and opens the page only once the sheet has gone and finished sliding away", () => {
		mountSheet();
		const open = vi.fn();
		leaveFor!(open);
		expect(leave).toHaveBeenCalledTimes(1);
		expect(open).not.toHaveBeenCalled();

		// Still on screen, mid-dismissal: the page must not be pushed yet.
		vi.advanceTimersByTime(SHEET_DISMISS_MS * 2);
		expect(open).not.toHaveBeenCalled();

		sheetGone();
		vi.advanceTimersByTime(SHEET_DISMISS_MS - 1);
		expect(open).not.toHaveBeenCalled();
		vi.advanceTimersByTime(1);
		expect(open).toHaveBeenCalledTimes(1);
	});

	it("ignores a second request while the first is waiting", () => {
		mountSheet();
		const first = vi.fn();
		const second = vi.fn();
		leaveFor!(first);
		leaveFor!(second);
		expect(leave).toHaveBeenCalledTimes(1);
		sheetGone();
		vi.advanceTimersByTime(SHEET_DISMISS_MS);
		expect(first).toHaveBeenCalledTimes(1);
		expect(second).not.toHaveBeenCalled();
	});

	it("opens nothing when the sheet closes without a request", () => {
		mountSheet();
		sheetGone();
		vi.advanceTimersByTime(SHEET_DISMISS_MS);
		expect(leave).not.toHaveBeenCalled();
	});

	it("on Android, where the route is a page, leaves and opens at once", () => {
		env.os = "android";
		mountSheet();
		const open = vi.fn();
		leaveFor!(open);
		expect(leave).toHaveBeenCalledTimes(1);
		expect(open).toHaveBeenCalledTimes(1);
	});
});
