import { createElement, type ComponentProps } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	ActivityIndicator: "ActivityIndicator",
	Pressable: "Pressable",
	Text: "Text",
	TextInput: "TextInput",
	View: "View",
	StyleSheet: { create: <T,>(styles: T) => styles, hairlineWidth: 0.5 },
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
import { touchTarget } from "../tokens";
import { RequestCard } from "./RequestCard";
import type { RequestDockModel, RequestDockPage } from "./requestDockModel";

type Props = ComponentProps<typeof RequestCard>;
let renderer: ReactTestRenderer | undefined;
afterEach(() => { if (renderer) act(() => renderer!.unmount()); renderer = undefined; vi.clearAllMocks(); });

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
const style = (node: ReactTestInstance) => Object.assign({}, ...[node.props.style({ pressed: false })].flat(3).filter(Boolean));

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

it("gives the pager, Skip, close and send full touch targets", () => {
	render({ model: model({ pages: [page({ required: false, freeText: { name: "question_1_custom", placeholder: "Something else", numeric: false } }), page()] }) });
	for (const label of ["Previous question", "Next question"]) {
		expect(style(byLabel(label))).toMatchObject({ width: touchTarget, height: touchTarget });
	}
	expect(style(byLabel("Skip this question"))).toMatchObject({ minWidth: touchTarget, minHeight: touchTarget });
	act(() => renderer!.root.findByType("TextInput" as never).props.onChangeText("staging"));
	expect(style(byLabel("Send answer"))).toMatchObject({ width: touchTarget, height: touchTarget });
	act(() => renderer!.unmount());
	render();
	expect(style(byLabel("Dismiss and type instead"))).toMatchObject({ width: touchTarget, height: touchTarget });
});

it("fires one haptic for an answer, when it lands", async () => {
	render();
	await act(async () => byLabel("main").props.onPress());
	expect(haptics.success).toHaveBeenCalledOnce();
	expect(haptics.tap).not.toHaveBeenCalled();
	expect(haptics.select).not.toHaveBeenCalled();
});
