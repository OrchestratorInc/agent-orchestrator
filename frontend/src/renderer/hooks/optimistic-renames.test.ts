import { beforeEach, describe, expect, it } from "vitest";
import {
	beginOptimisticRename,
	isOptimisticRenameTracked,
	optimisticRenamesVersion,
	renamedTitle,
	renameReadStarted,
	resetOptimisticRenamesForTest,
	settleOptimisticRename,
	subscribeOptimisticRenames,
} from "./optimistic-renames";

const KEY = "session:local:s-1";

beforeEach(() => resetOptimisticRenamesForTest());

describe("optimistic renames", () => {
	it("tells subscribers synchronously, so lists redraw with the editor closing", () => {
		const seen: number[] = [];
		const unsubscribe = subscribeOptimisticRenames(() => seen.push(optimisticRenamesVersion()));
		beginOptimisticRename(KEY, "New", "Old");
		expect(seen).toHaveLength(1);
		expect(renamedTitle(KEY)).toBe("New");
		unsubscribe();
	});

	it("shows the new title over any read while the rename is in flight", () => {
		const before = renameReadStarted();
		beginOptimisticRename(KEY, "New", "Old");
		const during = renameReadStarted();
		expect(renamedTitle(KEY, before)).toBe("New");
		expect(renamedTitle(KEY, during)).toBe("New");
	});

	it("keeps an acknowledged title over reads begun before the acknowledgement only", () => {
		const token = beginOptimisticRename(KEY, "New", "Old");
		const staleRead = renameReadStarted();
		expect(settleOptimisticRename(KEY, token, true)).toEqual({ title: "New", latest: true });
		const freshRead = renameReadStarted();

		expect(renamedTitle(KEY)).toBeUndefined();
		expect(renamedTitle(KEY, staleRead)).toBe("New");
		// A read begun after the acknowledgement is the server's answer, even a different name.
		expect(renamedTitle(KEY, freshRead)).toBeUndefined();
	});

	it("never lets an older rename's answer replace a newer title", () => {
		const first = beginOptimisticRename(KEY, "First", "Old");
		const second = beginOptimisticRename(KEY, "Second", "Old");

		expect(settleOptimisticRename(KEY, first, true)).toEqual({ title: "Second", latest: false });
		expect(renamedTitle(KEY)).toBe("Second");
		expect(settleOptimisticRename(KEY, second, true)).toEqual({ title: "Second", latest: true });
	});

	it("ignores an older answer that lands after the newer one", () => {
		const first = beginOptimisticRename(KEY, "First", "Old");
		const second = beginOptimisticRename(KEY, "Second", "Old");
		const staleRead = renameReadStarted();

		expect(settleOptimisticRename(KEY, second, true)?.latest).toBe(true);
		expect(settleOptimisticRename(KEY, first, true)).toBeUndefined();
		expect(renamedTitle(KEY, staleRead)).toBe("Second");
	});

	it("rolls a failure back to the title from before when nothing else is in flight", () => {
		const read = renameReadStarted();
		const token = beginOptimisticRename(KEY, "New", "Old");

		expect(settleOptimisticRename(KEY, token, false)).toEqual({ title: "Old", latest: true });
		expect(isOptimisticRenameTracked(KEY)).toBe(false);
		expect(renamedTitle(KEY, read)).toBeUndefined();
	});

	it("rolls a failure back to the rename still in flight before it", () => {
		const first = beginOptimisticRename(KEY, "First", "Old");
		const second = beginOptimisticRename(KEY, "Second", "Old");

		expect(settleOptimisticRename(KEY, second, false)).toEqual({ title: "First", latest: true });
		expect(renamedTitle(KEY)).toBe("First");
		expect(settleOptimisticRename(KEY, first, true)).toEqual({ title: "First", latest: true });
	});

	it("keeps the newer title when a superseded rename fails", () => {
		const first = beginOptimisticRename(KEY, "First", "Old");
		beginOptimisticRename(KEY, "Second", "Old");

		expect(settleOptimisticRename(KEY, first, false)).toEqual({ title: "Second", latest: false });
		expect(renamedTitle(KEY)).toBe("Second");
	});

	it("rolls a failure back to the last acknowledged title", () => {
		const first = beginOptimisticRename(KEY, "First", "Old");
		const staleRead = renameReadStarted();
		settleOptimisticRename(KEY, first, true);
		const second = beginOptimisticRename(KEY, "Second", "First");
		const laterRead = renameReadStarted();

		expect(settleOptimisticRename(KEY, second, false)).toEqual({ title: "First", latest: true });
		// A read begun before the first acknowledgement still shows it; a later one is the server's.
		expect(renamedTitle(KEY, staleRead)).toBe("First");
		expect(renamedTitle(KEY, laterRead)).toBeUndefined();
	});

	it("keeps keys apart", () => {
		beginOptimisticRename(KEY, "New", "Old");
		expect(renamedTitle("session:local:s-2")).toBeUndefined();
	});
});
