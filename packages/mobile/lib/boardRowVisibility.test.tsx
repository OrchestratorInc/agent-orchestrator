import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { useContext } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BoardRowScope, createVisibleRows, VisibleRowsContext } from "./boardRowVisibility";
import { LatticeActiveContext } from "./chat/latticeActivity";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

describe("visible board rows", () => {
	it("counts every row as on screen until the list first reports", () => {
		const rows = createVisibleRows();
		expect(rows.visible("a")).toBe(true);
		rows.set(new Set(["b"]));
		expect(rows.visible("a")).toBe(false);
		expect(rows.visible("b")).toBe(true);
	});

	it("tells a row only when its own visibility changes", () => {
		const rows = createVisibleRows();
		rows.set(new Set(["a", "b"]));
		const a = vi.fn();
		const b = vi.fn();
		rows.subscribe("a", a);
		rows.subscribe("b", b);
		rows.set(new Set(["a", "c"]));
		expect(a).not.toHaveBeenCalled();
		expect(b).toHaveBeenCalledOnce();
	});
});

describe("a board row's scope", () => {
	let renderer: ReactTestRenderer | undefined;
	afterEach(() => {
		act(() => renderer?.unmount());
		renderer = undefined;
	});
	const seen: boolean[] = [];
	function Lattice() {
		seen.push(useContext(LatticeActiveContext));
		return null;
	}

	it("lets the row's lattice animate only while the row is on screen", () => {
		const rows = createVisibleRows();
		seen.length = 0;
		act(() => {
			renderer = create(<VisibleRowsContext.Provider value={rows}><BoardRowScope rowKey="worker-1"><Lattice /></BoardRowScope></VisibleRowsContext.Provider>);
		});
		expect(seen.at(-1)).toBe(true);
		act(() => rows.set(new Set(["worker-2"])));
		expect(seen.at(-1)).toBe(false);
		act(() => rows.set(new Set(["worker-1"])));
		expect(seen.at(-1)).toBe(true);
	});
});
