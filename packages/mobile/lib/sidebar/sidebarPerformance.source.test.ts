import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const sidebar = readFileSync(fileURLToPath(new URL("./Sidebar.tsx", import.meta.url)), "utf8");

// Home stuttered while scrolling: it mounted every project and session at once,
// each with a native menu, and redrew all of them on every store update, which
// ticks constantly while agents work.
describe("home list performance", () => {
	it("virtualizes home's rows, as the board before it did", () => {
		expect(sidebar).toContain("<FlatList\n\t\t\t\tdata={homeItems}");
		expect(sidebar).not.toContain("<ScrollView style={styles.scroll} contentContainerStyle={[styles.scrollContent, { paddingBottom: FAB_HEIGHT");
	});

	it("redraws a row only when something it shows changes", () => {
		expect(sidebar).toContain("}, sameSessionRow);");
		expect(sidebar).toContain("}, sameProjectRow);");
	});

	it("keeps the rows' callbacks the same function across store updates", () => {
		expect(sidebar).toContain("}, [close, openPage, openSession]);");
		expect(sidebar).not.toContain("}, [app, close, openPage, openSession]);");
		expect(sidebar).toContain("const cancelRename = useCallback(() => setRenamingKey(undefined), []);");
		expect(sidebar).not.toContain("onCancelRename={() => setRenamingKey(undefined)}");
	});

	it("measures the list once rather than every session row", () => {
		expect(sidebar).toContain("onLayout={onListLayout}");
		expect(sidebar).not.toContain("<View style={indented ? styles.indented : null} onLayout={slot.onLayout}>");
	});

	// The drawer's Pinned rows got no width after the per-row measuring went,
	// and iOS's menu host let a long title run past the drawer's edge.
	it("gives the drawer's rows a measured width too, so long titles truncate", () => {
		expect(sidebar).toContain("			width={rowWidth}\n");
		expect(sidebar).toContain("showsVerticalScrollIndicator={false} onLayout={onListLayout}>");
	});
});
