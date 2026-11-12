import { act, create, type ReactTestRenderer, type ReactTestInstance } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ fontScale: 1 }));

vi.mock("react-native", async () => {
	const React = await import("react");
	return {
		StyleSheet: { create: (styles: object) => styles },
		useWindowDimensions: () => ({ fontScale: fixtures.fontScale }),
		View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("View", props, children),
	};
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 24, bottom: 24, left: 0, right: 0 }) }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => new Proxy({}, { get: (_target, property) => `#${String(property)}` }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Badge: host("Badge"),
		BadgedBox: Object.assign(host("BadgedBox"), { Badge: host("BadgeSlot") }),
		Box: host("Box"),
		Column: host("Column"),
		Host: host("ComposeHost"),
		Icon: host("Icon"),
		Row: host("Row"),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	background: (color: string) => ({ background: color }),
	clickable: (handler: () => void) => ({ clickable: handler }),
	clip: () => ({ clip: true }),
	dropShadow: () => ({ dropShadow: true }),
	paddingAll: (all: number) => ({ paddingAll: all }),
	selectable: (selected: boolean, handler: () => void, role: string) => ({ selectable: { selected, handler, role } }),
	selectableGroup: () => ({ selectableGroup: true }),
	semantics: (config: object) => ({ semantics: config }),
	Shapes: { Circle: "circle", RoundedCorner: (radius: number) => `rounded:${radius}` },
	size: (width: number, height: number) => ({ size: [width, height] }),
	testID: (tag: string) => ({ testID: tag }),
}));
import { BoardTabBar } from "./BoardTabBar.android";
import type { BoardTabBarProps } from "./BoardTabBar.types";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.fontScale = 1;
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

type Modifier = Record<string, unknown>;
const modifier = (node: ReactTestInstance, key: string) =>
	(node.props.modifiers as Modifier[]).find((entry) => key in entry)?.[key];
const withTag = (tag: string) =>
	renderer!.root.find((node) => Array.isArray(node.props.modifiers) && modifier(node, "testID") === tag);

function render(props: Partial<BoardTabBarProps> = {}) {
	const all: BoardTabBarProps = { selected: "(workers)", onSelect: vi.fn(), waiting: 0, onNewTask: vi.fn(), ...props };
	act(() => { renderer = create(<BoardTabBar {...all} />); });
	return all;
}

describe("BoardTabBar on Android", () => {
	// iOS's layout: the tabs in one pill at the start, New task at the end.
	it("lays Workers and Projects out at the start and New task at the end", () => {
		render();
		const [tabs, newTask] = renderer!.root.findAllByType("ComposeHost" as never);
		expect(tabs.findAllByType("ComposeText" as never).map((text) => text.props.children)).toEqual(["Workers", "Projects"]);
		expect(newTask.findByType("Icon" as never).props.source).toBe("drawable:add");
		expect(modifier(newTask.findByType("Box" as never), "semantics")).toEqual({ contentDescription: "New task" });
	});

	it("selects tabs as tabs, for TalkBack, and starts a task from the round button", () => {
		const props = render({ selected: "(projects)" });
		const workers = modifier(withTag("board-tab-workers"), "selectable") as { selected: boolean; handler: () => void; role: string };
		const projects = modifier(withTag("board-tab-projects"), "selectable") as { selected: boolean };
		expect([workers.selected, projects.selected, workers.role]).toEqual([false, true, "tab"]);
		act(() => workers.handler());
		expect(props.onSelect).toHaveBeenCalledWith("(workers)");
		act(() => (modifier(withTag("spawn-worker"), "clickable") as () => void)());
		expect(props.onNewTask).toHaveBeenCalledOnce();
	});

	// Workers waiting on a person: the one count worth lighting a tab for.
	it.each([[0, []], [3, ["3"]], [120, ["99+"]]])("badges Workers with %s waiting as %j", (waiting, badges) => {
		render({ waiting: waiting as number });
		expect(renderer!.root.findAllByType("Badge" as never).map((badge) => badge.findByType("ComposeText" as never).props.children)).toEqual(badges);
	});

	// At 2× text the labels overflow their tabs; the icons stay, named for TalkBack.
	it("drops the labels past the chrome text cap and names the tabs instead", () => {
		fixtures.fontScale = 2;
		render();
		expect(renderer!.root.findAllByType("ComposeText" as never)).toHaveLength(0);
		expect(modifier(withTag("board-tab-projects"), "semantics")).toEqual({ contentDescription: "Projects" });
	});
});
