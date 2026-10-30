import React from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";

const fixtures = vi.hoisted(() => ({ theme: new Proxy({}, { get: () => "#000000" }) }));

vi.mock("expo-router", () => ({ useFocusEffect: vi.fn(), useRouter: () => ({ push: vi.fn() }) }));

vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator", Alert: { alert: vi.fn() }, AppState: { addEventListener: () => ({ remove: vi.fn() }) },
	FlatList: "FlatList", Platform: { OS: "ios" }, Pressable: "Pressable", RefreshControl: "RefreshControl",
	StyleSheet: { create: (styles: object) => styles }, Text: "Text", View: "View",
}));
vi.mock("react-native-reanimated", () => ({ LayoutAnimationConfig: ({ children }: { children: React.ReactNode }) => children }));
vi.mock("./store", () => ({ useApp: () => ({ projects: [], allProjects: [], currentHostId: "mac", connection: "open", hostStates: [] }) }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme),
}));
vi.mock("./ui", () => ({ ListSectionHeader: "ListSectionHeader" }));
vi.mock("./worker-list-row", () => ({ WorkerListRow: "WorkerListRow" }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./archived-worker-row", () => ({ ArchivedWorkerRow: "ArchivedWorkerRow" }));
// Inline archive only, as from a machine that still sends it with the poll.
vi.mock("./archive/useArchivedSessions", () => ({
	useArchivedSessions: (_sources: unknown, options: { inline: DashboardSession[] }) => ({
		rows: options.inline, total: options.inline.length, loading: false, error: null, hasMore: false, loadMore: vi.fn(), retry: vi.fn(),
	}),
}));

import { WorkerBoardList, type BoardRow } from "./worker-board-list";

let renderer: ReactTestRenderer;

function session(id: string, status: DashboardSession["status"]): DashboardSession {
	return {
		id, projectId: "project-1", status, mode: "chat", branch: null,
		issueId: null, issueTitle: null, userPrompt: null, displayName: null, summary: null,
		createdAt: "2026-10-09T00:00:00Z", lastActivityAt: "2026-10-09T00:00:00Z",
	};
}

function render(sessions: DashboardSession[], { archiveFooter = true } = {}) {
	act(() => {
		renderer = create(
			<WorkerBoardList sessions={sessions} contentBottomInset={0} refreshing={false} onRefresh={() => {}}
				archiveFooter={archiveFooter} ListEmptyComponent={React.createElement("EmptyState")} />,
		);
	});
	return renderer.root.findByType("FlatList" as never);
}

// The FlatList is a host stub, so it never renders the header it was handed.
function headerTree() {
	const list = renderer.root.findByType("FlatList" as never);
	let header!: ReactTestRenderer;
	act(() => { header = create(list.props.ListHeaderComponent); });
	return header.root;
}

beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(() => { act(() => renderer?.unmount()); vi.unstubAllGlobals(); });

describe("board with only archived workers", () => {
	// The Archive row was the board's only row, so the list was never empty and
	// its empty state never drew: a lone "Archive 8" at the top of a blank screen.
	it("shows the empty state, with Archive beneath it rather than as the only row", () => {
		const list = render([session("old-1", "terminated"), session("old-2", "terminated")]);
		expect(list.props.data).toEqual([]);
		expect(list.props.ListEmptyComponent).toBeNull();
		expect(list.props.ListHeaderComponentStyle).toEqual({ flexGrow: 1 });

		const header = headerTree();
		expect(header.findAllByType("EmptyState" as never)).toHaveLength(1);
		expect(header.findAllByProps({ accessibilityLabel: "Archive, 2 sessions" })).toHaveLength(1);
	});

	it("lists archived rows under the footer once Archive is opened", () => {
		render([session("old-1", "terminated"), session("old-2", "terminated")]);
		const toggle = headerTree().findByProps({ accessibilityLabel: "Archive, 2 sessions" });
		act(() => toggle.props.onPress());
		const data = renderer.root.findByType("FlatList" as never).props.data as BoardRow[];
		expect(data.map((row) => row.kind)).toEqual(["archived", "archived"]);
	});

	it("keeps Archive as an ordinary row below live workers", () => {
		const list = render([session("live", "working"), session("old", "terminated")]);
		const kinds = (list.props.data as BoardRow[]).map((row) => row.kind);
		expect(kinds.at(-1)).toBe("archive");
		expect(kinds).toContain("session");
		expect(list.props.ListHeaderComponentStyle).toBeUndefined();
		expect(list.props.ListEmptyComponent).not.toBeNull();
	});

	// A project page also renders this list, and its empty state says "No workers
	// yet": wrong under a project's history. Only the Workers tab opts in.
	it("keeps Archive as the only row on a board that does not opt in", () => {
		const list = render([session("old-1", "terminated"), session("old-2", "terminated")], { archiveFooter: false });
		expect((list.props.data as BoardRow[]).map((row) => row.kind)).toEqual(["archive"]);
		expect(list.props.ListHeaderComponent).toBeUndefined();
		expect(list.props.ListHeaderComponentStyle).toBeUndefined();
		expect(list.props.contentContainerStyle.flexGrow).toBeUndefined();
	});
});
