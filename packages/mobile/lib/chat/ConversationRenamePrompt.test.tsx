import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

type Button = { text: string; style?: string; isPreferred?: boolean; onPress?: (value?: string) => void };

const fixtures = vi.hoisted(() => ({
	prompt: vi.fn(),
	alert: vi.fn(),
	haptics: { success: vi.fn(), error: vi.fn() },
	nativeState: (value: unknown) => ({ value }),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Alert: { prompt: fixtures.prompt, alert: fixtures.alert },
		Platform: { OS: "android" },
		StyleSheet: { create: (styles: unknown) => styles },
		View: host("View"),
	};
});
vi.mock("../haptics", () => ({ haptics: fixtures.haptics }));
vi.mock("../connectionError", () => ({ userFacingError: (cause: Error, fallback: string) => cause?.message || fallback }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemeState: () => ({ scheme: "dark" }) };
});
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		AlertDialog: Object.assign(host("AlertDialog"), {
			Title: host("AlertDialog.Title"),
			Text: host("AlertDialog.Text"),
			ConfirmButton: host("AlertDialog.ConfirmButton"),
			DismissButton: host("AlertDialog.DismissButton"),
		}),
		Host: host("Host"),
		OutlinedTextField: Object.assign(host("OutlinedTextField"), {
			Label: host("OutlinedTextField.Label"),
			SupportingText: host("OutlinedTextField.SupportingText"),
		}),
		Text: host("ComposeText"),
		TextButton: host("TextButton"),
		useNativeState: (value: unknown) => fixtures.nativeState(value),
	};
});

import { ConversationRenamePrompt } from "./ConversationRenamePrompt";
import { ConversationRenamePrompt as AndroidRenamePrompt } from "./ConversationRenamePrompt.android";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	fixtures.prompt.mockReset();
	fixtures.alert.mockReset();
	fixtures.haptics.success.mockReset();
	fixtures.haptics.error.mockReset();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

function render(element: React.ReactElement) {
	act(() => {
		renderer = create(element);
	});
	return renderer!;
}

const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); });
const promptButtons = (call = 0) => fixtures.prompt.mock.calls[call][2] as Button[];
const save = (call: number, value: string) => promptButtons(call).find((button) => button.text === "Save")!.onPress!(value);

describe("iOS rename prompt", () => {
	it("asks with the system text prompt, prefilled", () => {
		render(<ConversationRenamePrompt request={{ id: 1, initialTitle: "Fix login", onRename: vi.fn() }} onClose={vi.fn()} />);
		expect(fixtures.prompt).toHaveBeenCalledTimes(1);
		const [title, message, buttons, type, initial] = fixtures.prompt.mock.calls[0];
		expect(title).toBe("Rename conversation");
		expect(message).toBe("Use a short name that makes this worker easy to find.");
		expect(type).toBe("plain-text");
		expect(initial).toBe("Fix login");
		expect((buttons as Button[]).map((button) => [button.text, button.style])).toEqual([["Cancel", "cancel"], ["Save", undefined]]);
	});

	it("asks once per request, even when the screen remounts it", () => {
		const request = { id: 1, initialTitle: "Fix login", onRename: vi.fn() };
		const tree = render(<ConversationRenamePrompt request={request} onClose={vi.fn()} />);
		act(() => tree.update(<></>));
		act(() => tree.update(<ConversationRenamePrompt request={request} onClose={vi.fn()} />));
		expect(fixtures.prompt).toHaveBeenCalledTimes(1);
		act(() => tree.update(<ConversationRenamePrompt request={{ ...request, id: 2 }} onClose={vi.fn()} />));
		expect(fixtures.prompt).toHaveBeenCalledTimes(2);
	});

	it("renames to the trimmed title and closes", async () => {
		const onRename = vi.fn(async () => {});
		const onClose = vi.fn();
		render(<ConversationRenamePrompt request={{ id: 1, initialTitle: "Fix login", onRename }} onClose={onClose} />);
		save(0, "  Release follow-up  ");
		await flush();
		expect(onRename).toHaveBeenCalledWith("Release follow-up");
		expect(fixtures.haptics.success).toHaveBeenCalledTimes(1);
		expect(onClose).toHaveBeenCalledTimes(1);
	});

	it("treats an empty title as Cancel", async () => {
		const onRename = vi.fn();
		const onClose = vi.fn();
		render(<ConversationRenamePrompt request={{ id: 1, initialTitle: "Fix login", onRename }} onClose={onClose} />);
		save(0, "   ");
		await flush();
		expect(onRename).not.toHaveBeenCalled();
		expect(onClose).toHaveBeenCalledTimes(1);
	});

	it("explains a failure and offers the prompt again with what was typed", async () => {
		const onRename = vi.fn(async () => { throw new Error("The machine refused the name."); });
		const onClose = vi.fn();
		render(<ConversationRenamePrompt request={{ id: 1, initialTitle: "Fix login", onRename }} onClose={onClose} />);
		save(0, "New name");
		await flush();
		expect(fixtures.haptics.error).toHaveBeenCalledTimes(1);
		expect(onClose).not.toHaveBeenCalled();
		const [title, message, buttons] = fixtures.alert.mock.calls[0];
		expect(title).toBe("Couldn't rename this conversation.");
		expect(message).toBe("The machine refused the name.");
		(buttons as Button[]).find((button) => button.text === "Try again")!.onPress!();
		expect(fixtures.prompt).toHaveBeenCalledTimes(2);
		expect(fixtures.prompt.mock.calls[1][4]).toBe("New name");
	});
});

describe("Android rename dialog", () => {
	it("is a Material dialog with an outlined field and text buttons", () => {
		const root = render(<AndroidRenamePrompt request={{ id: 1, initialTitle: "Fix login", onRename: vi.fn() }} onClose={vi.fn()} />).root;
		const field = root.findByType("OutlinedTextField" as never);
		expect(field.props).toMatchObject({ autoFocus: true, singleLine: true, maxLength: 100, value: { value: "Fix login" } });
		const [confirm, dismiss] = root.findAllByType("TextButton" as never);
		expect(confirm.props.enabled).toBe(true);
		expect(dismiss.props.enabled).toBe(true);
	});

	it("draws nothing without a request", () => {
		expect(render(<AndroidRenamePrompt request={undefined} onClose={vi.fn()} />).toJSON()).toBeNull();
	});

	it("disables Save for an empty title", () => {
		const root = render(<AndroidRenamePrompt request={{ id: 1, initialTitle: "Fix login", onRename: vi.fn() }} onClose={vi.fn()} />).root;
		act(() => root.findByType("OutlinedTextField" as never).props.onValueChange("   "));
		expect(root.findAllByType("TextButton" as never)[0].props.enabled).toBe(false);
	});

	it("holds while the rename runs and keeps the reason when it fails", async () => {
		let reject: (cause: Error) => void = () => {};
		const onRename = vi.fn(() => new Promise<void>((_resolve, fail) => { reject = fail; }));
		const onClose = vi.fn();
		const root = render(<AndroidRenamePrompt request={{ id: 1, initialTitle: "Fix login", onRename }} onClose={onClose} />).root;
		act(() => root.findAllByType("TextButton" as never)[0].props.onClick());
		expect(onRename).toHaveBeenCalledWith("Fix login");
		const dialog = root.findByType("AlertDialog" as never);
		expect(dialog.props.properties).toEqual({ dismissOnBackPress: false, dismissOnClickOutside: false });
		act(() => dialog.props.onDismissRequest());
		expect(onClose).not.toHaveBeenCalled();
		await act(async () => { reject(new Error("The machine refused the name.")); await Promise.resolve(); });
		expect(root.findByType("OutlinedTextField" as never).props.isError).toBe(true);
		expect(root.findByType("OutlinedTextField.SupportingText" as never).findByType("ComposeText" as never).props.children).toBe("The machine refused the name.");
		expect(onClose).not.toHaveBeenCalled();
	});

	it("closes once renamed", async () => {
		const onClose = vi.fn();
		const root = render(<AndroidRenamePrompt request={{ id: 1, initialTitle: "Fix login", onRename: async () => {} }} onClose={onClose} />).root;
		await act(async () => { root.findAllByType("TextButton" as never)[0].props.onClick(); await Promise.resolve(); });
		expect(fixtures.haptics.success).toHaveBeenCalledTimes(1);
		expect(onClose).toHaveBeenCalledTimes(1);
	});
});
