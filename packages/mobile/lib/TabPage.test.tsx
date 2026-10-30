import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ android: true }));

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: object) => styles }, View: "View" }));
vi.mock("./frost", async () => {
	const React = await import("react");
	return {
		BAR_SEAM: 16,
		SEAM_STOPS: [[0, 0.9], [1, 0]],
		Frost: "Frost",
		useTabBarFrost: () => fixtures.android ? { inset: 104, frost: React.createElement("Frost", { from: "bottom" }) } : { inset: 0, frost: null },
	};
});

import { TabPage } from "./TabPage";

const Page = () => null;

let renderer: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	fixtures.android = true;
});

function render() {
	act(() => { renderer = create(<TabPage><Page /></TabPage>); });
	return renderer!.root;
}

describe("TabPage", () => {
	it("ends the page at Android's clear tab bar, frosts the bar, and seams the top bar", () => {
		const root = render();
		const page = root.findAllByType("View" as never)[0];
		expect(Object.assign({}, ...page.props.style).paddingBottom).toBe(104);
		expect(root.findAllByType("Frost" as never).map((frost) => frost.props.from)).toEqual(["top", "bottom"]);
		expect(root.findAllByType("Frost" as never)[0].props.style).toMatchObject({ position: "absolute", top: 0, height: 16 });
	});

	it("is a plain page where the system's bars inset it", () => {
		fixtures.android = false;
		const root = render();
		expect(Object.assign({}, ...root.findAllByType("View" as never)[0].props.style).paddingBottom).toBe(0);
		expect(root.findAllByType("Frost" as never)).toHaveLength(0);
	});
});
