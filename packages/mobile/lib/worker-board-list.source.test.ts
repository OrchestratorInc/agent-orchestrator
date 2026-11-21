import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function source(relativePath: string): string {
	return readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");
}

const list = source("./worker-board-list.tsx");
const board = source("../app/(tabs)/(workers)/index.tsx");

describe("worker board list identity", () => {
	// Swapping the project filter in place left the board showing two lists at
	// once: recycled cells of the old grouping under the new data, and the empty
	// state drawn on top of rows that were still on screen. A different set of
	// workers is a different list, so it is mounted as one.
	it("remounts the whole animation boundary without retaining outgoing rows", () => {
		expect(list).toContain("<LayoutAnimationConfig key={`${identityKey}:${fontScale}`} skipEntering skipExiting>");
		expect(list).not.toContain("<FlatList\n\t\t\t\t\tref={listRef}\n\t\t\t\t\tkey={identityKey}");
		expect(board).toContain("const boardIdentityKey = `${workerEnvironmentId}|${workerProjectId}`;");
		expect(board).toContain("key={boardIdentityKey}");
		expect(board).toContain("identityKey={boardIdentityKey}");
	});

	// Memoised rows kept the old text size's measurements after a live change.
	it("rebuilds the list when the text size changes", () => {
		expect(list).toContain("const { fontScale } = useWindowDimensions();");
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
	// a turning Lucide loader, which only imitated the system's indicator. A
	// starting row does not breathe, and was left with that loader standing still.
	it("shows the system spinner in a working or starting row's status slot", () => {
		expect(row).toMatch(/visual\.breathing \|\| glyph === "loader"\s*\?\s*<Spinner size=\{iconSize\.xs\} color=\{visual\.color\} \/>/);
		expect(spinner).toContain("<ActivityIndicator");
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

describe("worker row machine", () => {
	const archived = source("./archived-worker-row.tsx");

	// A board whose rows are all on one machine names it once at its head; the
	// same name on every row only crowded the details. Archived rows too.
	it("leaves each row's machine off when the page names the one machine", () => {
		expect(list).toContain("showMachine ? sessionMachineIdentity(session) : undefined");
		expect(archived).toContain("showMachine ? sessionMachineIdentity(session) : undefined");
		expect(list.match(/showMachine=\{showMachine\}/g)).toHaveLength(2);
		expect(board).toContain("showMachine={!boardMachine}");
	});
});
