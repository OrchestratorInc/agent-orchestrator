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
		expect(list).toContain("<LayoutAnimationConfig key={identityKey} skipEntering skipExiting>");
		expect(list).not.toContain("<FlatList\n\t\t\t\t\tref={listRef}\n\t\t\t\t\tkey={identityKey}");
		expect(board).toContain("const boardIdentityKey = `${workerEnvironmentId}|${workerProjectId}`;");
		expect(board).toContain("key={boardIdentityKey}");
		expect(board).toContain("identityKey={boardIdentityKey}");
	});
});

describe("worker search", () => {
	// The board has no search: the filter menu narrows it. Nor does it bring
	// back the dock that rode the keyboard by hand.
	it("has no search field, in the header or riding the keyboard", () => {
		expect(board).not.toContain("<Stack.SearchBar");
		expect(board).not.toContain("react-native-keyboard-controller");
		expect(board).not.toContain("keyboardAnimation");
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

describe("board chrome", () => {
	// The pill, Archive header and archive status share the app's press,
	// glyph and colour rules rather than each inventing its own.
	it("draws system symbols, platform press feedback and theme colours", () => {
		expect(list).not.toContain("<Feather");
		expect(list).not.toMatch(/opacity: 0\.\d/);
		expect(list).not.toContain('"#000"');
		expect(list).not.toContain("<ActivityIndicator");
		expect(list).toContain('const feedback = usePressFeedback("button", { tone: "onAccent" });');
		expect(list).toContain("borderRadius: radius.pill");
	});

	// Archive heads a section like the board's other headers, at their footnote
	// step: no header on the board drops below 13pt or mixes sizes with another.
	it("sets the Archive header at the section headers' footnote step", () => {
		expect(list).not.toMatch(/type\.caption[12]/);
		expect(list).toMatch(/archiveLabel: \{[^}]*fontSize: type\.footnote\.fontSize/);
		expect(list).toMatch(/archiveCount: \{[^}]*fontSize: type\.footnote\.fontSize/);
		expect(source("./ui.tsx")).toMatch(/listSectionLabel: \{[^}]*fontSize: type\.footnote\.fontSize/);
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

describe("board tab roots", () => {
	// A coloured view that is not a stacking context is mounted empty, with its
	// children hoisted after it. As a tab's root it became the page's first
	// native view, ahead of the list, so the large title and the tab bar had no
	// scroll view to follow and stayed expanded over the rows.
	it.each(["../app/(tabs)/(workers)/index.tsx", "../app/(tabs)/(projects)/projects.tsx", "../app/prs/index.tsx"])(
		"leaves the page background to the stack in %s",
		(path) => {
			expect(source(path)).toMatch(/\bscreen: \{ flex: 1 \},/);
		},
	);
});
