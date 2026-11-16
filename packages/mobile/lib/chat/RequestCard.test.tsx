import { createElement, type ComponentProps } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
const { keyboard } = vi.hoisted(() => ({ keyboard: { isVisible: false, height: 0 } }));
vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	ActivityIndicator: "ActivityIndicator",
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	Text: "Text",
	TextInput: "TextInput",
	View: "View",
	StyleSheet: { create: <T,>(styles: T) => styles, hairlineWidth: 0.5 },
	useWindowDimensions: () => ({ width: 390, height: 844 }),
}));
vi.mock("react-native-keyboard-controller", () => ({
	useKeyboardState: (select: (state: typeof keyboard) => unknown) => select(keyboard),
}));
vi.mock("react-native-gesture-handler", () => {
	const fling = () => ({ direction: () => fling(), onEnd: () => fling(), runOnJS: () => fling() });
	return { Directions: { LEFT: 2, RIGHT: 1 }, Gesture: { Fling: fling, Race: () => ({}) }, GestureDetector: ({ children }: { children: unknown }) => children };
});
vi.mock("react-native-reanimated", () => {
	const slide = { duration: () => ({}) };
	return { default: { View: "AnimatedView" }, SlideInLeft: slide, SlideInRight: slide, SlideOutLeft: slide, SlideOutRight: slide };
});
vi.mock("../haptics", () => ({ haptics: { select: vi.fn(), success: vi.fn(), error: vi.fn(), tap: vi.fn() } }));
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => true }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (t: typeof darkTheme) => unknown) => factory(darkTheme) };
});

import { haptics } from "../haptics";
import { RequestCard } from "./RequestCard";
import type { RequestDockModel, RequestDockPage } from "./requestDockModel";

type Props = ComponentProps<typeof RequestCard>;
let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	if (renderer) act(() => renderer!.unmount());
	renderer = undefined;
	vi.clearAllMocks();
	keyboard.isVisible = false;
	keyboard.height = 0;
});

const page = (over: Partial<RequestDockPage> = {}): RequestDockPage => ({
	question: "Which branch?",
	options: [{ id: "main", label: "main" }, { id: "develop", label: "develop", description: "The integration branch" }],
	propertyName: "question_1",
	multi: false,
	required: true,
	...over,
});
const model = (over: Partial<RequestDockModel> = {}): RequestDockModel => ({
	kind: "input",
	title: "Agent needs input",
	pages: [page()],
	requestId: "req-1",
	sequence: 4,
	canAnswerInline: true,
	busy: false,
	...over,
});

function render(over: Partial<Props> = {}) {
	const props: Props = {
		model: model(),
		onDecide: vi.fn(async () => {}),
		onResolveInput: vi.fn(async () => {}),
		onShow: vi.fn(),
		onDismiss: vi.fn(),
		...over,
	};
	act(() => { renderer = create(createElement(RequestCard, props)); });
	return props;
}

const byLabel = (label: string): ReactTestInstance => renderer!.root.find((node) => (node.type as unknown) === "Pressable" && node.props.accessibilityLabel === label);

it("offers an answer of several as a radio, and says which one was chosen", async () => {
	const props = render({ model: model({ pages: [page(), page({ propertyName: "question_2", question: "Which remote?" })] }) });
	const develop = byLabel("develop");
	expect(develop.props.accessibilityRole).toBe("radio");
	expect(develop.props.accessibilityHint).toBe("The integration branch");
	expect(develop.props.accessibilityState).toEqual({ disabled: false, checked: false });
	await act(async () => develop.props.onPress());
	await act(async () => byLabel("Previous question").props.onPress());
	expect(byLabel("develop").props.accessibilityState).toEqual({ disabled: false, checked: true });
	expect(props.onResolveInput).not.toHaveBeenCalled();
});

it("toggles several answers as checkboxes and sends them as a list", async () => {
	const props = render({ model: model({ pages: [page({ multi: true, options: [{ id: "read", label: "Read" }, { id: "write", label: "Write" }] })] }) });
	const send = () => byLabel("Send answers");
	expect(send().props.disabled).toBe(true);
	expect(byLabel("Read").props.accessibilityRole).toBe("checkbox");
	act(() => byLabel("Read").props.onPress());
	act(() => byLabel("Write").props.onPress());
	act(() => byLabel("Read").props.onPress());
	expect(byLabel("Write").props.accessibilityState).toEqual({ disabled: false, checked: true });
	expect(props.onResolveInput).not.toHaveBeenCalled();
	await act(async () => send().props.onPress());
	expect(props.onResolveInput).toHaveBeenCalledWith("req-1", "accept", { question_1: ["write"] });
});

it("keeps an approval's decisions as buttons", () => {
	const props = render({ model: model({ kind: "approval", title: "Approval needed", pages: [page({ propertyName: undefined, options: [{ id: "allow", label: "Allow" }] })] }) });
	const allow = byLabel("Allow");
	expect(allow.props.accessibilityRole).toBe("button");
	expect(allow.props.accessibilityState).toEqual({ disabled: false });
	act(() => allow.props.onPress());
	expect(props.onDecide).toHaveBeenCalledWith("req-1", "allow");
});

it("fires one haptic for an answer, when it lands", async () => {
	render();
	await act(async () => byLabel("main").props.onPress());
	expect(haptics.success).toHaveBeenCalledOnce();
	expect(haptics.tap).not.toHaveBeenCalled();
	expect(haptics.select).not.toHaveBeenCalled();
});

const LONG_COMMAND = Array.from({ length: 40 }, (_, line) => `echo "step ${line}"`).join("; ");
const longApproval = () => model({
	kind: "approval",
	title: "Approval needed",
	pages: [page({ question: LONG_COMMAND, propertyName: undefined, options: [{ id: "allow", label: "Allow" }, { id: "deny", label: "Deny" }] })],
});
const insideScroll = (node: ReactTestInstance) => {
	for (let parent = node.parent; parent; parent = parent.parent) if ((parent.type as unknown) === "ScrollView") return true;
	return false;
};
const cardMaxHeight = () => {
	const style = renderer!.root.findAll((node) => (node.type as unknown) === "View")[0].props.style as object[];
	return Object.assign({}, ...style).maxHeight as number;
};

it("scrolls a long approval command while its decisions and close stay pinned outside it", () => {
	render({ model: longApproval() });
	expect(insideScroll(renderer!.root.findByProps({ children: LONG_COMMAND }))).toBe(true);
	expect(insideScroll(byLabel("Allow"))).toBe(false);
	expect(insideScroll(byLabel("Deny"))).toBe(false);
	expect(insideScroll(byLabel("Dismiss and type instead"))).toBe(false);
});

it("caps the card at half the screen, and at half of what the keyboard leaves", () => {
	render({ model: longApproval() });
	expect(cardMaxHeight()).toBe(422);
	act(() => renderer!.unmount());
	keyboard.isVisible = true;
	keyboard.height = 336;
	render({ model: longApproval() });
	expect(cardMaxHeight()).toBe(254);
});

it("scrolls a question's options with it and pins the typed answer and the send for several answers", () => {
	render({ model: model({ pages: [page({ freeText: { name: "question_1_custom", placeholder: "Type your answer…", numeric: false } })] }) });
	expect(insideScroll(byLabel("develop"))).toBe(true);
	expect(insideScroll(renderer!.root.findByProps({ accessibilityLabel: "Type your answer…" }))).toBe(false);
	act(() => renderer!.unmount());
	render({ model: model({ pages: [page({ multi: true })] }) });
	expect(insideScroll(byLabel("main"))).toBe(true);
	expect(insideScroll(byLabel("Send answers"))).toBe(false);
});
