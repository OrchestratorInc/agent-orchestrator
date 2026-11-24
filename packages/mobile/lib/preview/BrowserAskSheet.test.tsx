import { createElement, type ReactNode } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BrowserCommand, BrowserDialog } from "../browserTransport";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: unknown) => styles }, Alert: { alert: vi.fn() } }));
vi.mock("@expo/ui/swift-ui", async () => {
	const doubles = (await import("../nativeFormTestDoubles")).swiftUIDouble();
	const host = (name: string) => ({ children, ...props }: { children?: ReactNode }) => createElement(name, props, children);
	const withSlots = (name: string, slots: string[]) => Object.assign(host(name), Object.fromEntries(slots.map((slot) => [slot, host(`${name}.${slot}`)])));
	return {
		...doubles,
		Spacer: host("Spacer"),
		Alert: withSlots("Alert", ["Actions", "Message", "Trigger"]),
		ConfirmationDialog: withSlots("ConfirmationDialog", ["Actions", "Message", "Trigger"]),
	};
});
vi.mock("@expo/ui/swift-ui/modifiers", async () => {
	const { modifierDouble } = await import("../nativeFormTestDoubles");
	return modifierDouble(["autocorrectionDisabled", "frame", "textContentType", "textInputAutocapitalization"]);
});
vi.mock("@expo/ui", async () => {
	const { createElement } = await import("react");
	return { Host: ({ children, ...props }: { children?: ReactNode }) => createElement("Host", props, children) };
});
vi.mock("../ThemeProvider", () => ({ useThemeState: () => ({ scheme: "dark" }) }));

import { BrowserAskSheet, resetBrowserAskKeys } from "./BrowserAskSheet";

let screen: ReactTestRenderer | undefined;
let answers: Array<[BrowserDialog, BrowserCommand | null]>;

function ask(dialog: BrowserDialog | null, pickers?: Parameters<typeof BrowserAskSheet>[0]["pickers"]) {
	const element = <BrowserAskSheet dialog={dialog} pickers={pickers} onAnswer={(asked: BrowserDialog, command: BrowserCommand | null) => answers.push([asked, command])} />;
	act(() => { if (screen) screen.update(element); else screen = create(element); });
}
const all = (type: string): ReactTestInstance[] => screen!.root.findAll((node) => (node.type as unknown) === type);
const press = async (label: string) => { await act(async () => all("Button").find((node) => node.props.label === label)!.props.onPress()); };

beforeEach(() => {
	answers = [];
	resetBrowserAskKeys();
});
afterEach(() => {
	act(() => screen?.unmount());
	screen = undefined;
	vi.restoreAllMocks();
});

describe("BrowserAskSheet on iOS", () => {
	it("answers an alert, a confirm and a leave-page question", async () => {
		const alert: BrowserDialog = { kind: "alert", message: "Saved" };
		ask(alert);
		expect(all("Alert")[0]!.props).toMatchObject({ title: "The page says", isPresented: true });
		await press("OK");
		const confirm: BrowserDialog = { kind: "confirm", message: "Delete it?" };
		ask(confirm);
		await press("Cancel");
		const leave: BrowserDialog = { kind: "beforeunload", message: "" };
		ask(leave);
		expect(all("Alert")[0]!.props.title).toBe("Leave this page?");
		await press("Leave");
		expect(answers).toEqual([
			[alert, { type: "dialog", payload: { accept: true } }],
			[confirm, { type: "dialog", payload: { accept: false } }],
			[leave, { type: "dialog", payload: { accept: true } }],
		]);
	});

	it("anchors each presentation to a one-point trigger, as SwiftUI needs", () => {
		ask({ kind: "confirm", message: "Sure?" });
		expect(all("Alert.Trigger")[0]!.findAll((node) => (node.type as unknown) === "Spacer")).toHaveLength(1);
		ask({ kind: "select", id: "s", selected: 0, options: [{ label: "A" }] });
		expect(all("ConfirmationDialog.Trigger")[0]!.findAll((node) => (node.type as unknown) === "Spacer")).toHaveLength(1);
		expect(all("Host")[0]!.props).toMatchObject({ colorScheme: "dark", style: { width: 1, height: 1 } });
	});

	it("goes away when the host closes the dialog or a newer one replaces it", () => {
		const first: BrowserDialog = { kind: "confirm", message: "First?" };
		ask(first);
		expect(all("Alert")).toHaveLength(1);
		const second: BrowserDialog = { kind: "alert", message: "Second" };
		ask(second);
		expect(all("Alert")).toHaveLength(1);
		expect(all("Text").map((node) => node.props.children)).toEqual(["Second"]);
		ask(null);
		expect(all("Alert")).toHaveLength(0);
		expect(all("ConfirmationDialog")).toHaveLength(0);
		expect(answers).toEqual([]);
	});

	it("counts a dismissal as declining, once", async () => {
		const confirm: BrowserDialog = { kind: "confirm", message: "Sure?" };
		ask(confirm);
		const alert = all("Alert")[0]!;
		await act(async () => { alert.props.onIsPresentedChange(false); alert.props.onIsPresentedChange(false); });
		expect(answers).toEqual([[confirm, { type: "dialog", payload: { accept: false } }]]);
	});

	it("answers a prompt with what was typed, starting from the page's default", async () => {
		const prompt: BrowserDialog = { kind: "prompt", message: "Your name?", defaultPrompt: "Ada" };
		ask(prompt);
		const field = all("TextField")[0]!;
		expect(field.props.text.get()).toBe("Ada");
		act(() => field.props.onTextChange("Grace"));
		await press("OK");
		expect(answers).toEqual([[prompt, { type: "dialog", payload: { accept: true, promptText: "Grace" } }]]);
	});

	it("auth password uses secure entry and is never logged", async () => {
		const logged: unknown[] = [];
		for (const method of ["log", "info", "warn", "error", "debug"] as const) vi.spyOn(console, method).mockImplementation((...args) => { logged.push(...args); });
		const auth: BrowserDialog = { kind: "auth", origin: "https://intranet.example", realm: "Staff", failed: true };
		ask(auth);
		expect(all("Alert")[0]!.props.title).toBe("Sign in to intranet.example");
		const [username] = all("TextField");
		const [password] = all("SecureField");
		expect(password).toBeDefined();
		// Found on the Simulator: iOS offered to save the page's password to the Keychain.
		expect(password!.props.modifiers).toEqual([{ $type: "textContentType", args: ["oneTimeCode"] }]);
		expect(username!.props.modifiers).toEqual(expect.arrayContaining([{ $type: "autocorrectionDisabled", args: [true] }, { $type: "textInputAutocapitalization", args: ["never"] }]));
		act(() => { username!.props.onTextChange("ada"); password!.props.onTextChange("s3cret-pw"); });
		await press("Sign in");
		expect(answers).toEqual([[auth, { type: "auth", payload: { origin: "https://intranet.example", username: "ada", password: "s3cret-pw" } }]]);
		expect(JSON.stringify(logged)).not.toContain("s3cret");
		expect(all("SecureField")).toHaveLength(0);
	});

	it("offers a select's enabled options, marking the current one", async () => {
		const select: BrowserDialog = { kind: "select", id: "s1", selected: 0, options: [{ label: "Red" }, { label: "Green", disabled: true }, { label: "Blue" }] };
		ask(select);
		expect(all("Button").map((node) => node.props.label)).toEqual(["✓ Red", "Blue", "Cancel"]);
		await press("Blue");
		expect(answers).toEqual([[select, { type: "select", payload: { id: "s1", index: 2 } }]]);
	});

	it("picks files from Photos or Files for the page's input", async () => {
		const pickers = {
			launchImageLibraryAsync: vi.fn(async () => ({ canceled: false, assets: [{ uri: "file:///p.jpg", fileName: "p.jpg", mimeType: "image/jpeg" }] })),
			getDocumentAsync: vi.fn(async () => ({ canceled: true, assets: null })),
		};
		const chooser: BrowserDialog = { kind: "filechooser", multiple: false, accept: "image/*", folder: false };
		ask(chooser, pickers);
		expect(all("Button").map((node) => node.props.label)).toEqual(["Photo Library", "Choose File", "Cancel"]);
		await press("Photo Library");
		expect(answers).toEqual([[chooser, { type: "files", payload: { folder: false, files: [{ uri: "file:///p.jpg", name: "p.jpg", type: "image/jpeg" }] } }]]);
	});

	it("keeps the picked files when choosing a source also dismisses its dialog", async () => {
		let pick!: (result: { canceled: boolean; assets: Array<{ uri: string; fileName: string }> }) => void;
		const pickers = {
			launchImageLibraryAsync: vi.fn(() => new Promise<{ canceled: boolean; assets: Array<{ uri: string; fileName: string }> }>((resolve) => { pick = resolve; })),
			getDocumentAsync: vi.fn(async () => ({ canceled: true, assets: null })),
		};
		const chooser: BrowserDialog = { kind: "filechooser", multiple: false, accept: "", folder: false };
		ask(chooser, pickers);
		const dialog = all("ConfirmationDialog")[0]!;
		await press("Photo Library");
		// SwiftUI dismisses the dialog for the tap, while the picker is still open.
		await act(async () => dialog.props.onIsPresentedChange(false));
		expect(answers).toEqual([]);
		await act(async () => pick({ canceled: false, assets: [{ uri: "file:///p.jpg", fileName: "p.jpg" }] }));
		expect(answers).toEqual([[chooser, { type: "files", payload: { folder: false, files: [{ uri: "file:///p.jpg", name: "p.jpg" }] } }]]);
	});

	it("cancels the chooser when its source dialog is dismissed without a choice", async () => {
		const chooser: BrowserDialog = { kind: "filechooser", multiple: false, accept: "", folder: false };
		ask(chooser);
		await act(async () => all("ConfirmationDialog")[0]!.props.onIsPresentedChange(false));
		expect(answers).toEqual([[chooser, { type: "files", payload: { files: [], folder: false } }]]);
	});

	it("offers only Files for an input that takes no images, and cancels with no files", async () => {
		const chooser: BrowserDialog = { kind: "filechooser", multiple: true, accept: ".pdf", folder: false };
		ask(chooser);
		expect(all("Button").map((node) => node.props.label)).toEqual(["Choose Files", "Cancel"]);
		await press("Cancel");
		expect(answers).toEqual([[chooser, { type: "files", payload: { files: [], folder: false } }]]);
	});
});
