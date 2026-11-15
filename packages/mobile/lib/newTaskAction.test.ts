import { describe, expect, it, vi } from "vitest";
import { setNewTaskHandler, startNewTask } from "./newTaskAction";

describe("the tab bar's New task button", () => {
	it("starts a task the focused page's way, and plainly once it leaves", () => {
		const page = vi.fn();
		const fallback = vi.fn();
		const withdraw = setNewTaskHandler(page);
		startNewTask(fallback);
		expect(page).toHaveBeenCalledOnce();
		expect(fallback).not.toHaveBeenCalled();
		withdraw();
		startNewTask(fallback);
		expect(fallback).toHaveBeenCalledOnce();
	});

	// Focus moves to the next page before the last one's cleanup runs.
	it("keeps a newer page's handler when an older one withdraws", () => {
		const older = vi.fn();
		const newer = vi.fn();
		const withdrawOlder = setNewTaskHandler(older);
		const withdrawNewer = setNewTaskHandler(newer);
		withdrawOlder();
		startNewTask(vi.fn());
		expect(newer).toHaveBeenCalledOnce();
		withdrawNewer();
	});
});
