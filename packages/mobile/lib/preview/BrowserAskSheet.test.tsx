import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BrowserCommand, BrowserDialog } from "../browserTransport";

type Button = { text: string; style?: string; onPress?: (value?: unknown) => void };
const native = vi.hoisted(() => ({
	alert: vi.fn<(title: string, message?: string, buttons?: Button[], options?: object) => void>(),
	prompt: vi.fn<(title: string, message?: string, buttons?: Button[], type?: string, defaultValue?: string) => void>(),
	sheet: vi.fn<(options: { options: string[]; cancelButtonIndex: number; disabledButtonIndices?: number[]; title?: string }, callback: (index: number) => void) => void>(),
}));

vi.mock("react-native", () => ({
	Alert: { alert: native.alert, prompt: native.prompt },
	ActionSheetIOS: { showActionSheetWithOptions: native.sheet },
}));

import { BrowserAskSheet as BrowserAsk } from "./BrowserAskSheet";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

let screen: ReactTestRenderer | undefined;
let answers: Array<[BrowserDialog, BrowserCommand | null]>;

function ask(dialog: BrowserDialog, pickers?: Parameters<typeof BrowserAsk>[0]["pickers"]) {
	const element = <BrowserAsk dialog={dialog} pickers={pickers} onAnswer={(asked: BrowserDialog, command: BrowserCommand | null) => answers.push([asked, command])} />;
	act(() => { if (screen) screen.update(element); else screen = create(element); });
}
const buttons = (mock: { mock: { lastCall?: unknown[] } }, at = 2) => (mock.mock.lastCall![at] as Button[]);
const press = (list: Button[], text: string, value?: unknown) => act(() => list.find((button) => button.text === text)!.onPress!(value));

beforeEach(() => {
	answers = [];
	native.alert.mockReset();
	native.prompt.mockReset();
	native.sheet.mockReset();
});
afterEach(() => {
	act(() => screen?.unmount());
	screen = undefined;
	vi.restoreAllMocks();
});

describe("BrowserAsk on iOS", () => {
	it("answers an alert, a confirm and a leave-page question", () => {
		const alert = { kind: "alert", message: "Saved" } as const;
		ask(alert);
		expect(native.alert).toHaveBeenLastCalledWith("The page says", "Saved", expect.any(Array), { cancelable: false });
		press(buttons(native.alert), "OK");

		const confirm = { kind: "confirm", message: "Delete it?" } as const;
		ask(confirm);
		press(buttons(native.alert), "Cancel");

		const leave = { kind: "beforeunload", message: "" } as const;
		ask(leave);
		expect(native.alert.mock.lastCall![0]).toBe("Leave this page?");
		press(buttons(native.alert), "Leave");

		expect(answers).toEqual([
			[alert, { type: "dialog", payload: { accept: true } }],
			[confirm, { type: "dialog", payload: { accept: false } }],
			[leave, { type: "dialog", payload: { accept: true } }],
		]);
	});

	it("answers a prompt with what was typed, starting from the page's default", () => {
		const prompt = { kind: "prompt", message: "Your name?", defaultPrompt: "Ada" } as const;
		ask(prompt);
		expect(native.prompt).toHaveBeenLastCalledWith("The page asks", "Your name?", expect.any(Array), "plain-text", "Ada");
		press(buttons(native.prompt), "OK", "Grace");
		expect(answers).toEqual([[prompt, { type: "dialog", payload: { accept: true, promptText: "Grace" } }]]);
	});

	it("auth password uses secure entry and is never logged", () => {
		const logged: unknown[] = [];
		for (const method of ["log", "info", "warn", "error", "debug"] as const) vi.spyOn(console, method).mockImplementation((...args) => { logged.push(...args); });
		const auth = { kind: "auth", origin: "https://intranet.example", realm: "Staff", failed: true } as const;
		ask(auth);
		// The system's login prompt: a username field and a secure password field.
		expect(native.prompt).toHaveBeenLastCalledWith(
			"Sign in to intranet.example",
			"That username or password didn't work.\nThe site says: Staff",
			expect.any(Array),
			"login-password",
		);
		press(buttons(native.prompt), "Sign in", { login: "ada", password: "s3cret-pw" });
		expect(answers).toEqual([[auth, { type: "auth", payload: { origin: "https://intranet.example", username: "ada", password: "s3cret-pw" } }]]);
		expect(JSON.stringify(logged)).not.toContain("s3cret");
	});

	it("cancels a sign-in", () => {
		const auth = { kind: "auth", origin: "https://a.example", failed: false } as const;
		ask(auth);
		press(buttons(native.prompt), "Cancel");
		expect(answers).toEqual([[auth, { type: "auth", payload: { origin: "https://a.example", cancel: true } }]]);
	});

	it("offers a select's options, marking the current one and disabling what the page disabled", () => {
		const select: BrowserDialog = { kind: "select", id: "s1", selected: 0, options: [{ label: "Red" }, { label: "Green", disabled: true }, { label: "Blue" }] };
		ask(select);
		const [options, choose] = native.sheet.mock.lastCall!;
		expect(options).toMatchObject({ options: ["✓ Red", "Green", "Blue", "Cancel"], cancelButtonIndex: 3, disabledButtonIndices: [1] });
		act(() => choose(2));
		expect(answers).toEqual([[select, { type: "select", payload: { id: "s1", index: 2 } }]]);
	});

	it("answers a cancelled select with nothing", () => {
		const select: BrowserDialog = { kind: "select", id: "s1", selected: 0, options: [{ label: "Red" }] };
		ask(select);
		act(() => native.sheet.mock.lastCall![1](1));
		expect(answers).toEqual([[select, null]]);
	});

	it("picks files from Photos or Files for the page's input", async () => {
		const pickers = {
			launchImageLibraryAsync: vi.fn(async () => ({ canceled: false, assets: [{ uri: "file:///p.jpg", fileName: "p.jpg", mimeType: "image/jpeg" }] })),
			getDocumentAsync: vi.fn(async () => ({ canceled: true, assets: null })),
		};
		const chooser = { kind: "filechooser", multiple: false, accept: "image/*", folder: false } as const;
		ask(chooser, pickers);
		expect(native.sheet.mock.lastCall![0]).toMatchObject({ options: ["Photo Library", "Choose File", "Cancel"], cancelButtonIndex: 2 });
		await act(async () => native.sheet.mock.lastCall![1](0));
		expect(answers).toEqual([[chooser, { type: "files", payload: { folder: false, files: [{ uri: "file:///p.jpg", name: "p.jpg", type: "image/jpeg" }] } }]]);
	});

	it("offers only Files for an input that takes no images", () => {
		ask({ kind: "filechooser", multiple: true, accept: ".pdf", folder: false });
		expect(native.sheet.mock.lastCall![0]).toMatchObject({ options: ["Choose Files", "Cancel"], cancelButtonIndex: 1 });
		act(() => native.sheet.mock.lastCall![1](1));
		expect(answers[0]![1]).toEqual({ type: "files", payload: { files: [], folder: false } });
	});

	it("answers each dialog once", () => {
		const alert = { kind: "alert", message: "x" } as const;
		ask(alert);
		const list = buttons(native.alert);
		press(list, "OK");
		press(list, "OK");
		expect(answers).toHaveLength(1);
	});
});
