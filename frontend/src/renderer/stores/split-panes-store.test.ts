import { beforeEach, describe, expect, it } from "vitest";
import { leafKeys } from "../lib/pane-split";
import { SPLIT_GROUPS_STORAGE_KEY, useSplitPanesStore } from "./split-panes-store";

describe("split panes store", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useSplitPanesStore.setState({ groupsBySession: {} });
	});

	it("splits, removes and ungroups through its actions", () => {
		const store = useSplitPanesStore.getState();
		store.splitBeside("sess-1", "session", "sh-1", "right");
		store.splitBeside("sess-1", "sh-1", "sh-2", "bottom");
		let [group] = useSplitPanesStore.getState().groupsBySession["sess-1"]!;
		expect(leafKeys(group!.root)).toEqual(["session", "sh-1", "sh-2"]);

		store.removeTab("sess-1", "sh-2");
		[group] = useSplitPanesStore.getState().groupsBySession["sess-1"]!;
		expect(leafKeys(group!.root)).toEqual(["session", "sh-1"]);

		store.ungroup("sess-1", group!.id);
		expect(useSplitPanesStore.getState().groupsBySession["sess-1"]).toBeUndefined();
	});

	it("remembers layouts in storage and forgets a session left without groups", () => {
		useSplitPanesStore.getState().splitBeside("sess-1", "session", "sh-1", "right");
		const saved = JSON.parse(window.localStorage.getItem(SPLIT_GROUPS_STORAGE_KEY) ?? "{}");
		expect(leafKeys(saved["sess-1"][0].root)).toEqual(["session", "sh-1"]);

		useSplitPanesStore.getState().removeTab("sess-1", "sh-1");
		expect(window.localStorage.getItem(SPLIT_GROUPS_STORAGE_KEY)).toBeNull();
	});
});
