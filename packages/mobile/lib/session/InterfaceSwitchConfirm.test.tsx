import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ alert: vi.fn() }));

vi.mock("react-native", () => ({
	Alert: { alert: fixtures.alert },
	StyleSheet: { create: (styles: object) => styles },
}));
vi.mock("../ThemeProvider", () => ({ useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Button: host("Button"),
		ConfirmationDialog: Object.assign(host("ConfirmationDialog"), {
			Trigger: host("DialogTrigger"),
			Actions: host("DialogActions"),
			Message: host("DialogMessage"),
		}),
		Spacer: host("Spacer"),
		Text: host("Text"),
	};
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({ frame: (size: object) => ({ frame: size }) }));

import { InterfaceSwitchConfirm } from "./InterfaceSwitchConfirm";
import { InterfaceSwitchConfirm as AndroidInterfaceSwitchConfirm } from "./InterfaceSwitchConfirm.android";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.alert.mockReset();
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

describe("the iOS interface-switch question", () => {
	it("is a confirmation dialog with the destructive choice marked and a cancel", async () => {
		const onChoose = vi.fn();
		const onCancel = vi.fn();
		await act(async () => { renderer = create(<InterfaceSwitchConfirm message="Keep the same session." onChoose={onChoose} onCancel={onCancel} />); });
		const dialog = renderer!.root.findByType("ConfirmationDialog" as never);
		expect(dialog.props).toMatchObject({ title: "Switch to Chat?", titleVisibility: "visible", isPresented: true });
		expect(renderer!.root.findByType("DialogMessage" as never).findByType("Text" as never).props.children).toBe("Keep the same session.");
		const buttons = renderer!.root.findByType("DialogActions" as never).findAllByType("Button" as never);
		expect(buttons.map((button) => [button.props.label, button.props.role])).toEqual([
			["Finish, then switch", undefined],
			["Stop and switch", "destructive"],
			["Keep Terminal UI", "cancel"],
		]);
		act(() => buttons[0].props.onPress());
		act(() => buttons[1].props.onPress());
		expect(onChoose.mock.calls).toEqual([["drain"], ["interrupt"]]);
		act(() => dialog.props.onIsPresentedChange(false));
		expect(onCancel).toHaveBeenCalledOnce();
	});

	it("stays closed while nothing is asked", async () => {
		await act(async () => { renderer = create(<InterfaceSwitchConfirm message={null} onChoose={vi.fn()} onCancel={vi.fn()} />); });
		expect(renderer!.root.findByType("ConfirmationDialog" as never).props.isPresented).toBe(false);
	});
});

describe("the Android interface-switch question", () => {
	it("asks once per question with the system's three-button dialog", async () => {
		const onChoose = vi.fn();
		const onCancel = vi.fn();
		await act(async () => { renderer = create(<AndroidInterfaceSwitchConfirm message={null} onChoose={onChoose} onCancel={onCancel} />); });
		expect(fixtures.alert).not.toHaveBeenCalled();
		await act(async () => { renderer!.update(<AndroidInterfaceSwitchConfirm message="Waiting for input." onChoose={onChoose} onCancel={onCancel} />); });
		expect(fixtures.alert).toHaveBeenCalledOnce();
		const [title, message, buttons, options] = fixtures.alert.mock.calls[0];
		expect([title, message]).toEqual(["Switch to Chat?", "Waiting for input."]);
		expect(buttons.map((button: { text: string; style?: string }) => [button.text, button.style])).toEqual([
			["Keep Terminal UI", "cancel"],
			["Finish, then switch", undefined],
			["Stop and switch", "destructive"],
		]);
		buttons[2].onPress();
		expect(onChoose).toHaveBeenCalledWith("interrupt");
		expect(options.cancelable).toBe(true);
		options.onDismiss();
		expect(onCancel).toHaveBeenCalledOnce();
	});
});
