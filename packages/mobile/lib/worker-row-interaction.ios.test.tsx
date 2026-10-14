import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement, forwardRef, useImperativeHandle } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type { WorkerRowInteractionProps } from "./worker-row-interaction.types";

vi.mock("react-native", () => ({ View: "View" }));
vi.mock("react-native-gesture-handler", () => ({}));
vi.mock("react-native-gesture-handler/Swipeable", () => ({
	default: forwardRef(function NativeSwipeBoundary(props: Record<string, any>, ref) {
		useImperativeHandle(ref, () => ({ close: () => {} }));
		return createElement("SwipeBoundary", props, props.renderRightActions(), props.children);
	}),
}));
vi.mock("./worker-rename-context-menu", () => ({ WorkerRowContextMenu: "ContextMenu" }));
vi.mock("./haptics", () => ({ haptics: { select: vi.fn() } }));
vi.mock("@expo/ui", () => ({ Host: "NativeHost" }));
vi.mock("@expo/ui/swift-ui", () => ({ Button: "NativeButton", HStack: "NativeStack", Image: "NativeImage" }));
vi.mock("@expo/ui/swift-ui/modifiers", () => Object.fromEntries([
	"accessibilityIdentifier", "accessibilityLabel", "buttonBorderShape", "buttonStyle", "controlSize", "frame", "labelStyle", "tint",
].map((name) => [name, (value: unknown) => ({ [name]: value })])));
vi.mock("./ThemeProvider", () => ({ useThemeState: () => ({ scheme: "dark" }), useTheme: () => ({ textPrimary: "white", textSecondary: "gray" }) }));

import { WorkerRowInteraction } from "./worker-row-interaction.ios";
import { WorkerRowActions } from "./worker-row-actions.ios";

let renderer: ReactTestRenderer | undefined;
afterEach(() => { act(() => renderer?.unmount()); });
const props: WorkerRowInteractionProps = {
	sessionId: "worker", enabled: true, children: <></>, rightActions: null,
	shellStyle: {}, foregroundStyle: {}, rowStyle: {}, pressedStyle: {},
	accessibilityLabel: "Worker", accessibilityHint: "Swipe for actions", onPress() {},
	actions: [], onAction() {}, onSwipeOpen() {}, onSwipeClose() {},
};
function mount() {
	Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
	act(() => { renderer = create(<WorkerRowInteraction {...props} rightActions={
		<WorkerRowActions title="Task" pinned={false} onSetPinned={() => {}} onDelete={() => {}} />
	} />); });
	return renderer!;
}

it("does not mount native action controls for a resting row but keeps the rail measurable", () => {
	const view = mount();
	expect(view.root.findAllByType(WorkerRowActions)).toHaveLength(0);
	const rail = view.root.findByType("SwipeBoundary" as any).props.renderRightActions();
	expect(rail.props.style).toMatchObject({ width: 128 });
});

it("mounts actions at drag start and retains them until the closing animation finishes", () => {
	const view = mount();
	const boundary = () => view.root.findByType("SwipeBoundary" as any);
	act(() => boundary().props.onSwipeableOpenStartDrag("right"));
	expect(view.root.findAllByType(WorkerRowActions)).toHaveLength(1);
	act(() => boundary().props.onSwipeableWillOpen());
	// A close request starts movement; only its completion can remove controls.
	act(() => boundary().props.onSwipeableWillClose?.());
	expect(view.root.findAllByType(WorkerRowActions)).toHaveLength(1);
	act(() => boundary().props.onSwipeableClose());
	expect(view.root.findAllByType(WorkerRowActions)).toHaveLength(0);
});

it("releases native controls after a short swipe snaps closed without opening", () => {
	const view = mount();
	const boundary = () => view.root.findByType("SwipeBoundary" as any);
	act(() => boundary().props.onSwipeableOpenStartDrag("right"));
	expect(view.root.findAllByType(WorkerRowActions)).toHaveLength(1);
	act(() => boundary().props.onSwipeableClose());
	expect(view.root.findAllByType(WorkerRowActions)).toHaveLength(0);
});
