import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserCommand, BrowserControl, FocusedEditable } from "../browserTransport";

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	const TextInput = React.forwardRef((props: object, ref) => {
		React.useImperativeHandle(ref, () => ({ focus: vi.fn(), blur: vi.fn() }));
		return React.createElement("TextInput", props);
	});
	return { Platform: { OS: "ios" }, StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 }, Text: host("Text"), TextInput, View: host("View") };
});
vi.mock("react-native-keyboard-controller", async () => {
	const React = await import("react");
	return { KeyboardStickyView: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("KeyboardStickyView", props, children) };
});
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_target, name) => `theme.${String(name)}` });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});
vi.mock("../ui", async () => {
	const React = await import("react");
	return { Button: (props: object) => React.createElement("Button", props) };
});

import { PageKeyboard } from "./PageKeyboard";
import { PageTyping } from "./pageTyping";

let screen: ReactTestRenderer;
let sent: BrowserCommand[];
let typing: PageTyping;

function render(focused: FocusedEditable, control: BrowserControl = "human") {
	const element = <PageKeyboard typing={typing} focused={focused} control={control} connection={1} openRequest={0} />;
	act(() => { if (screen) screen.update(element); else screen = create(element); });
}
const field = () => screen.root.findAll((node) => (node.type as unknown) === "TextInput")[0];

function setup() {
	sent = [];
	typing = new PageTyping((command) => sent.push(command));
	screen = undefined as unknown as ReactTestRenderer;
}

afterEach(() => {
	act(() => screen?.unmount());
	vi.restoreAllMocks();
});

describe("PageKeyboard", () => {
	it("password field uses secure entry and redacts logs", () => {
		setup();
		const logged: unknown[] = [];
		for (const method of ["log", "info", "warn", "error", "debug"] as const) {
			vi.spyOn(console, method).mockImplementation((...args: unknown[]) => { logged.push(...args); });
		}
		render({ kind: "password" });
		const input = field()!;
		expect(input.props).toMatchObject({
			secureTextEntry: true,
			autoCorrect: false,
			spellCheck: false,
			autoComplete: "off",
			autoCapitalize: "none",
			textContentType: "none",
			importantForAutofill: "no",
		});

		act(() => input.props.onChangeText("s3cret-pass"));
		act(() => input.props.onSubmitEditing());

		// The value reaches the page, and nowhere else: no log, no label.
		expect(sent[0]).toEqual({ type: "input", payload: { kind: "text", value: "s3cret-pass" } });
		expect(JSON.stringify(logged)).not.toContain("s3cret");
		const labels = screen.root.findAll((node) => typeof node.props.accessibilityLabel === "string" || (node.type as unknown) === "Text");
		expect(JSON.stringify(labels.map((node) => [node.props.accessibilityLabel, node.props.children]))).not.toContain("s3cret");
	});

	it("opens for a field the person controls and closes when the agent acts", () => {
		setup();
		render({ kind: "search", value: "" });
		expect(field()?.props).toMatchObject({ keyboardType: "web-search", returnKeyType: "search", secureTextEntry: false });

		render({ kind: "search", value: "" }, "agent");
		expect(field()).toBeUndefined();
		render(null);
		expect(field()).toBeUndefined();
	});

	it("starts from the page's value and types only the change", () => {
		setup();
		render({ kind: "text", value: "draft" });
		expect(field()!.props.value).toBe("draft");
		act(() => field()!.props.onChangeText("draft!"));
		expect(sent).toEqual([{ type: "input", payload: { kind: "text", value: "!" } }]);
	});

	it("sends Backspace from an empty field, where the page may still hold text", () => {
		setup();
		render({ kind: "text" });
		act(() => field()!.props.onKeyPress({ nativeEvent: { key: "Backspace" } }));
		expect(sent.map((command) => (command.payload as { phase?: string; key?: string }).key)).toEqual(["Backspace", "Backspace"]);
	});

	it("puts the keyboard away on Done", () => {
		setup();
		render({ kind: "text", value: "" });
		const done = screen.root.findAll((node) => (node.type as unknown) === "Button")[0]!;
		act(() => done.props.onPress());
		expect(field()).toBeUndefined();
	});
});
