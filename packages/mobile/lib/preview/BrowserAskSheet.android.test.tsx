import { createElement, type ReactNode } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BrowserCommand, BrowserDialog } from "../browserTransport";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

type Button = { text: string; style?: string; onPress?: () => void };
const native = vi.hoisted(() => ({ alert: vi.fn<(title: string, message?: string, buttons?: Button[], options?: object) => void>() }));

vi.mock("react-native", () => ({ Alert: { alert: native.alert }, StyleSheet: { create: (styles: unknown) => styles } }));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const doubles = (await import("../nativeFormTestDoubles")).composeDouble();
	const host = (name: string) => ({ children, ...props }: { children?: ReactNode }) => createElement(name, props, children);
	const withSlots = (name: string, slots: string[]) => Object.assign(host(name), Object.fromEntries(slots.map((slot) => [slot, host(`${name}.${slot}`)])));
	return {
		...doubles,
		AlertDialog: withSlots("AlertDialog", ["Title", "Text", "ConfirmButton", "DismissButton"]),
		TextField: withSlots("TextField", ["Label", "Placeholder", "SupportingText"]),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", async () => {
	const { modifierDouble } = await import("../nativeFormTestDoubles");
	return modifierDouble(["alpha", "clickable", "fillMaxWidth", "height"]);
});
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_target, name) => `theme.${String(name)}` });
	return { useTheme: () => theme, useThemeState: () => ({ scheme: "dark", theme }) };
});

import { BrowserAskSheet, resetBrowserAskKeys } from "./BrowserAskSheet.android";

let screen: ReactTestRenderer | undefined;
let answers: Array<[BrowserDialog, BrowserCommand | null]>;

function ask(dialog: BrowserDialog | null) {
	const element = <BrowserAskSheet dialog={dialog} onAnswer={(asked: BrowserDialog, command: BrowserCommand | null) => answers.push([asked, command])} />;
	act(() => { if (screen) screen.update(element); else screen = create(element); });
}
const all = (type: string): ReactTestInstance[] => screen!.root.findAll((node) => (node.type as unknown) === type);
const buttonLabelled = (label: string) => all("TextButton").find((node) => node.findAll((child) => child.props.children === label).length > 0)!;
const type = (field: ReactTestInstance, value: string) => act(() => field.props.onValueChange(value));

beforeEach(() => {
	answers = [];
	native.alert.mockReset();
	resetBrowserAskKeys();
});
afterEach(() => {
	act(() => screen?.unmount());
	screen = undefined;
	vi.restoreAllMocks();
});

describe("BrowserAskSheet on Android", () => {
	it("auth password uses secure entry and is never logged", () => {
		const logged: unknown[] = [];
		for (const method of ["log", "info", "warn", "error", "debug"] as const) vi.spyOn(console, method).mockImplementation((...args) => { logged.push(...args); });
		const auth: BrowserDialog = { kind: "auth", origin: "https://intranet.example", failed: false };
		ask(auth);
		const [username, password] = all("TextField");
		expect(password!.props).toMatchObject({
			visualTransformation: "password",
			keyboardOptions: expect.objectContaining({ keyboardType: "password", autoCorrectEnabled: false }),
		});
		expect(username!.props.keyboardOptions).toMatchObject({ autoCorrectEnabled: false, capitalization: "none" });
		type(username!, "ada");
		type(password!, "s3cret-pw");
		act(() => buttonLabelled("Sign in").props.onClick());
		expect(answers).toEqual([[auth, { type: "auth", payload: { origin: "https://intranet.example", username: "ada", password: "s3cret-pw" } }]]);
		expect(JSON.stringify(logged)).not.toContain("s3cret");
		// Answered, the dialog goes, and the password with it.
		expect(all("TextField")).toHaveLength(0);
	});

	it("answers a prompt with its text, starting from the page's default", () => {
		const prompt: BrowserDialog = { kind: "prompt", message: "Your name?", defaultPrompt: "Ada" };
		ask(prompt);
		type(all("TextField")[0]!, "Grace");
		act(() => buttonLabelled("OK").props.onClick());
		expect(answers).toEqual([[prompt, { type: "dialog", payload: { accept: true, promptText: "Grace" } }]]);
	});

	it("lists a select's options; a disabled one can't be picked", () => {
		const select: BrowserDialog = { kind: "select", id: "s1", selected: 0, options: [{ label: "Red" }, { label: "Green", disabled: true }, { label: "Blue" }] };
		ask(select);
		const rows = all("ListItem");
		expect(rows).toHaveLength(3);
		expect(rows[1]!.props.modifiers).toEqual([{ $type: "alpha", args: [expect.any(Number)] }]);
		const clickable = rows[2]!.props.modifiers.find((modifier: { $type: string }) => modifier.$type === "clickable");
		act(() => clickable.args[0]());
		expect(answers).toEqual([[select, { type: "select", payload: { id: "s1", index: 2 } }]]);
	});

	it("answers alerts and where to pick files from in Material dialogs", () => {
		const confirm: BrowserDialog = { kind: "confirm", message: "Sure?" };
		ask(confirm);
		act(() => buttonLabelled("OK").props.onClick());
		const chooser: BrowserDialog = { kind: "filechooser", multiple: true, accept: "", folder: false };
		ask(chooser);
		expect(all("ListItem")).toHaveLength(2);
		act(() => buttonLabelled("Cancel").props.onClick());
		expect(answers).toEqual([
			[confirm, { type: "dialog", payload: { accept: true } }],
			[chooser, { type: "files", payload: { files: [], folder: false } }],
		]);
		expect(native.alert).not.toHaveBeenCalled();
	});

	// Found on the Android emulator: opening the system picker dismisses the
	// dialog, which cancelled the chooser before the picked files came back.
	it("keeps the picked files when the picker's opening dismisses the source dialog", async () => {
		let pick!: (result: { canceled: boolean; assets: Array<{ uri: string; name: string }> }) => void;
		const pickers = {
			launchImageLibraryAsync: vi.fn(async () => ({ canceled: true, assets: null })),
			getDocumentAsync: vi.fn(() => new Promise<{ canceled: boolean; assets: Array<{ uri: string; name: string }> }>((resolve) => { pick = resolve; })),
		};
		const chooser: BrowserDialog = { kind: "filechooser", multiple: false, accept: ".txt", folder: false };
		const element = <BrowserAskSheet dialog={chooser} pickers={pickers} onAnswer={(asked: BrowserDialog, command: BrowserCommand | null) => answers.push([asked, command])} />;
		act(() => { screen = create(element); });
		const dismiss = all("AlertDialog")[0]!.props.onDismissRequest as () => void;
		const row = all("ListItem")[0]!;
		await act(async () => row.props.modifiers.find((modifier: { $type: string }) => modifier.$type === "clickable").args[0]());
		await act(async () => dismiss());
		expect(answers).toEqual([]);
		await act(async () => pick({ canceled: false, assets: [{ uri: "file:///n.txt", name: "n.txt" }] }));
		expect(answers).toEqual([[chooser, { type: "files", payload: { files: [{ uri: "file:///n.txt", name: "n.txt" }], folder: false } }]]);
	});

	it("goes away when the host closes the dialog or a newer one replaces it", () => {
		ask({ kind: "confirm", message: "First?" });
		expect(all("AlertDialog")).toHaveLength(1);
		ask({ kind: "auth", origin: "https://a.example", failed: false });
		expect(all("AlertDialog")).toHaveLength(1);
		expect(all("TextField")).toHaveLength(2);
		ask(null);
		expect(all("AlertDialog")).toHaveLength(0);
		expect(answers).toEqual([]);
	});
});
