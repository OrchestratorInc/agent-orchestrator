import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function source(relativePath: string): string {
	return readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");
}

const list = source("./worker-board-list.tsx");
const board = source("../app/(tabs)/index.tsx");

describe("worker board list identity", () => {
	// Swapping the project filter in place left the board showing two lists at
	// once: recycled cells of the old grouping under the new data, and the empty
	// state drawn on top of rows that were still on screen. A different set of
	// workers is a different list, so it is mounted as one.
	it("remounts the whole animation boundary without retaining outgoing rows", () => {
		expect(list).toContain("<LayoutAnimationConfig key={identityKey} skipEntering skipExiting>");
		expect(list).not.toContain("<FlatList\n\t\t\t\t\tref={listRef}\n\t\t\t\t\tkey={identityKey}");
		expect(board).toContain("const boardIdentityKey = `${workerEnvironmentId}|${workerProjectId}|${query.trim()}`;");
		expect(board).toContain("key={boardIdentityKey}");
		expect(board).toContain("identityKey={boardIdentityKey}");
	});
});

describe("worker dock keyboard lift", () => {
	// The library's animated `height` is the keyboard's frame origin, which is
	// negative while the keyboard is up — its own avoiding view negates it before
	// use. Feeding it to a `max(…, 0)` gave a lift of exactly zero, which parked
	// the search field under the keyboard instead of above it.
	it("drives the lift from progress, not from the animated height", () => {
		expect(board).toContain("-keyboardAnimation.progress.value * workerDockLift(keyboardHeight, insets.bottom)");
		expect(board).not.toContain("keyboardAnimation.height.value");
	});
});

describe("worker row second line", () => {
	const row = source("./worker-list-row.tsx");

	// The line led with the branch unconditionally, so every row carried a worktree
	// path under its title. Dropping the branch outright then left rows with no PR
	// showing nothing at all, on exactly the sessions whose branch was the only
	// thing naming their worktree.
	it("falls back to the branch when the row has no pull request", () => {
		expect(row).toContain("const details = prs?.text ?? row.branch ?? \"\";");
		expect(row).not.toContain("const details = prs?.text ?? \"\";");
	});
});

describe("worker row working indicator", () => {
	const row = source("./worker-list-row.tsx");
	const ui = source("./ui.tsx");
	const spinner = source("./Spinner.tsx");

	// A still circle reads as stuck or decided rather than live. It shipped as a
	// bare glyph, then as a pulse (a waiting signal, not a working one), then as
	// a turning Lucide loader, which only imitated the system's indicator.
	it("shows the system spinner in a working row's status slot", () => {
		expect(row).toMatch(/visual\.breathing\s*\?\s*<Spinner size=\{iconSize\.xs\} color=\{visual\.color\} \/>/);
		expect(row).not.toContain("<Spinning");
		expect(row).not.toContain("<Breathing");
		expect(spinner).toContain("<ActivityIndicator");
		expect(ui).not.toContain("export function Spinning(");
	});

	// One decision, read inside the primitive, so a new consumer cannot forget
	// Reduce Motion.
	it("drives the dot's pulse off the motion rules", () => {
		expect(ui).toContain("export function useBreathing(");
		expect(ui).toContain("shouldBreathe(reduceMotion, enabled)");
		expect(ui).toContain("const pulse = useBreathing(breathing);");
	});
});

describe("board sections that cannot be folded", () => {
	const board = source("./worker-board-list.tsx");

	// "Needs you" is why the app was opened, "Pinned" is what the user put at the
	// top on purpose, and "Recent" is a plain separator for the normal worker list.
	// None should behave like an accordion or hide its rows behind a chevron.
	it("keeps every live worker section always open", () => {
		expect(board).toContain('const ALWAYS_OPEN = new Set<ListSection["zone"]>(["search", "needs_you", "pinned", "recent"]);');
		expect(board).toContain("const collapsible = !ALWAYS_OPEN.has(section.zone);");
		// The previous rule made everything but search foldable.
		expect(board).not.toContain('const collapsible = section.zone !== "search";');
	});
});
