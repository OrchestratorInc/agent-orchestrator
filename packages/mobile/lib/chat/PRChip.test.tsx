import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }) }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("react-native", () => ({ StyleSheet: { create: (styles: object) => styles }, Text: "Text" }));

import type { DashboardPR } from "../api";
import { workerRowPullRequest } from "../workerRowPullRequest";
import { prChipAccessibilityLabel, PRChipContent } from "./PRChip";

const pr = (number: number, state: "open" | "merged" | "closed"): DashboardPR => ({ number, url: "", state }) as DashboardPR;
let renderer: ReactTestRenderer | undefined;
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; });
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

describe("PRChipContent", () => {
	it("draws the +N after the status in the others' tone", () => {
		act(() => { renderer = create(<PRChipContent pr={pr(9137, "open")} more={{ count: 3, tone: "merged" }} />); });
		const more = renderer!.root.findAll((n) => (n.type as unknown) === "Text" && n.props.children === " +3");
		expect(more).toHaveLength(1);
		expect(JSON.stringify(more[0].props.style)).toContain("#purple");
	});

	it("draws no +N for one pull request", () => {
		act(() => { renderer = create(<PRChipContent pr={pr(9137, "open")} />); });
		expect(renderer!.root.findAll((n) => typeof n.props.children === "string" && n.props.children.startsWith(" +"))).toHaveLength(0);
	});

	it("draws a +N with no tone in the secondary text colour", () => {
		act(() => { renderer = create(<PRChipContent pr={pr(9137, "open")} more={{ count: 2, tone: null }} />); });
		const more = renderer!.root.find((n) => n.props.children === " +2");
		expect(JSON.stringify(more.props.style)).toContain("#textSecondary");
	});
});

describe("prChipAccessibilityLabel", () => {
	it("says what the +N counts", () => {
		const badge = workerRowPullRequest({ prs: [pr(9137, "open"), pr(9125, "merged"), pr(9120, "merged"), pr(9101, "merged")] })!;
		expect(prChipAccessibilityLabel(pr(9137, "open"), undefined, badge)).toMatch(/^Pull request 9137, .+, and 3 more merged$/);
		expect(prChipAccessibilityLabel(pr(9137, "open"))).toMatch(/^Pull request 9137, [^,]+$/);
	});
});
