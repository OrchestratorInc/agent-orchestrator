import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { HostSnapshot } from "./otherHosts";

const fixtures = vi.hoisted(() => ({ app: {} as Record<string, unknown>, now: 0, tap: vi.fn() }));

vi.mock("react-native", () => ({
	Pressable: "Pressable",
	Text: "Text",
	View: "View",
	StyleSheet: { create: (styles: object) => styles },
	Platform: { OS: "ios", select: (choices: { ios?: unknown; default?: unknown }) => choices.ios ?? choices.default },
}));
vi.mock("./store", () => ({ useApp: () => fixtures.app }));
vi.mock("./useNow", () => ({ useNow: () => fixtures.now }));
vi.mock("./haptics", () => ({ haptics: { tap: fixtures.tap } }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_target, key) => `#${String(key)}` });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});

import { FreshnessLine } from "./FreshnessLine";
import { EMPTY_LINK_TRACKER, recordPoll, UNREACHABLE_AFTER_FAILURES, UNREACHABLE_AFTER_MS, type LinkTracker } from "./linkHealth";

const NOW = 50_000_000;
const online = { connected: true, internet: true };

function snapshot(id: string, patch: Partial<HostSnapshot> = {}): HostSnapshot {
	return {
		hostId: id, name: id === "mac" ? "Studio" : id, appearance: { symbol: "laptop" }, endpoints: [], config: null, connection: "open", loading: false,
		error: null, errorStatus: null, projects: [], projectsKnown: true, sessions: [], orchestrators: [], orchestratorId: null,
		stats: {}, notificationsUnread: 0, lastSyncAt: NOW - 1_000, linkTracker: EMPTY_LINK_TRACKER, ...patch,
	};
}
function failing(count: number, span: number): LinkTracker {
	return Array.from({ length: count }, (_, i) => NOW - span + (i * span) / Math.max(1, count - 1))
		.reduce((tracker, at) => recordPoll(tracker, "no-answer", at), recordPoll(EMPTY_LINK_TRACKER, "ok", NOW - span - 8_000));
}

let renderer: ReactTestRenderer | undefined;
function render(props: { hostId?: string; onRetry?: () => void }) {
	act(() => { renderer = create(<FreshnessLine {...props} />); });
	return renderer!.root.findAll((node) => node.props.testID === "freshness-line" && (node.type as unknown) === "View")[0] as ReactTestInstance | undefined;
}
const text = (line: ReactTestInstance) => line.findAllByType("Text" as never)[0];
const dot = (line: ReactTestInstance) => line.find((node) => (node.type as unknown) === "View" && node.props.accessibilityElementsHidden === true);
const retry = (line: ReactTestInstance) => line.findAllByType("Pressable" as never)[0];

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.now = NOW;
	fixtures.tap.mockClear();
	fixtures.app = {
		hostStates: [snapshot("mac")],
		phoneNetwork: online,
		phoneOffline: false,
		linkHealth: "ok",
		primaryHostId: "mac",
		getPrimaryLastSyncAt: () => NOW - 1_000,
	};
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

describe("FreshnessLine", () => {
	it("says nothing while the machine is live", () => {
		expect(render({ hostId: "mac", onRetry: vi.fn() })).toBeUndefined();
	});

	it("names an unreachable machine in red, with its age and Retry, as a polite live region", () => {
		fixtures.app.hostStates = [snapshot("mac", { connection: "closed", linkTracker: failing(UNREACHABLE_AFTER_FAILURES, UNREACHABLE_AFTER_MS) })];
		fixtures.app.getPrimaryLastSyncAt = () => NOW - 180_000;
		const onRetry = vi.fn();
		const line = render({ hostId: "mac", onRetry })!;
		expect(text(line).props.children).toBe("Can't reach Studio · updated 3m ago");
		expect(text(line).props.accessibilityLiveRegion).toBe("polite");
		expect(line.props.accessibilityRole).toBe("alert");
		expect(dot(line).props.style).toContainEqual({ backgroundColor: "#red" });
		act(() => retry(line).props.onPress());
		expect(onRetry).toHaveBeenCalledOnce();
		expect(fixtures.tap).toHaveBeenCalledOnce();
	});

	// The selected machine's sync time comes from the store's unscoped getter,
	// not its snapshot, which trails the polls on a quiet board.
	it("reads the selected machine's sync time through the getter", () => {
		fixtures.app.hostStates = [snapshot("mac", { lastSyncAt: NOW - 600_000 })];
		expect(render({ hostId: "mac" })).toBeUndefined();
	});

	it("says it is reconnecting in amber after a single unanswered poll", () => {
		fixtures.app.hostStates = [snapshot("mac", { linkTracker: recordPoll(EMPTY_LINK_TRACKER, "no-answer", NOW - 2_000) })];
		const line = render({ hostId: "mac", onRetry: vi.fn() })!;
		expect(text(line).props.children).toBe("Reconnecting to Studio…");
		expect(dot(line).props.style).toContainEqual({ backgroundColor: "#amber" });
	});

	it("puts the phone being offline first, without Retry, on any page", () => {
		fixtures.app.phoneOffline = true;
		fixtures.app.getPrimaryLastSyncAt = () => NOW - 180_000;
		const line = render({ onRetry: vi.fn() })!;
		expect(text(line).props.children).toBe("You're offline · updated 3m ago");
		expect(retry(line)).toBeUndefined();
	});

	it("leaves a page with several machines to their dots", () => {
		fixtures.app.hostStates = [snapshot("mac", { connection: "closed", linkTracker: failing(UNREACHABLE_AFTER_FAILURES, UNREACHABLE_AFTER_MS) }), snapshot("pc")];
		expect(render({ onRetry: vi.fn() })).toBeUndefined();
	});
});
