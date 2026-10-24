import { CloudApiError } from "@aoagents/cloud-client";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const clipboard = vi.hoisted(() => vi.fn(async (_text: string) => {}));
vi.mock("expo-clipboard", () => ({ setStringAsync: clipboard }));
vi.mock("react-native", () => ({ View: "View", Text: "Text", Pressable: "Pressable", StyleSheet: { create: (styles: object) => styles } }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../ui", () => ({ Button: "Button" }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemedStyles: (make: (theme: typeof darkTheme) => unknown) => make(darkTheme) };
});
import { CloudSessionLimitNotice } from "./CloudSessionLimitNotice";

let renderer: ReactTestRenderer | undefined;
const failure = (requestId = "support-reference") => new CloudApiError(409, {
	error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", requestId,
	message: "Internal provider https://private.service/limit rejected Bearer private-token",
	details: { providerUrl: "https://private.service/limit", accessToken: "private-token" },
});
const text = () => renderer!.root.findAllByType("Text" as never).map(node => node.props.children).join(" ");
const button = (title: string) => renderer!.root.findByProps({ title });
const details = () => renderer!.root.findByProps({ accessibilityLabel: "Details", accessibilityRole: "button" });
const mount = async (error = failure()) => {
	await act(async () => { renderer = create(<CloudSessionLimitNotice error={error} onRetry={vi.fn()} busy={false} />); });
};
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); clipboard.mockReset(); clipboard.mockResolvedValue(undefined);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => { if (!String(args[0]).startsWith("react-test-renderer is deprecated")) error(...args); });
});
afterEach(async () => { if (renderer) await act(async () => renderer?.unmount()); renderer = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it("explains how to free organization capacity without showing technical diagnostics", async () => {
	await mount();
	expect(text()).toContain("Cloud session limit reached");
	expect(text()).toContain("This worker wasn’t created.");
	expect(text()).toContain("Archive an unused Cloud session");
	expect(details().props.accessibilityState.expanded).toBe(false);
	expect(text()).not.toContain("Paused sessions");
	expect(renderer!.root.findAllByProps({ title: "Copy support info" })).toHaveLength(0);
	await act(async () => details().props.onPress());
	expect(details().props.accessibilityState.expanded).toBe(true);
	expect(text()).toContain("organization");
	expect(text()).toContain("across all projects");
	expect(text()).toContain("Paused sessions still count");
	expect(text()).toContain("Manage Cloud sessions");
	expect(text()).toContain("cleanup may take a moment");
	expect(text()).not.toContain("HTTP");
	expect(text()).not.toContain("409");
	expect(text()).not.toContain("support-reference");
	expect(text()).not.toContain("SANDBOX_QUOTA_EXCEEDED");
	expect(text()).not.toContain("private.service");
	expect(text()).not.toContain("private-token");
	expect(clipboard).not.toHaveBeenCalled();
	await act(async () => details().props.onPress());
	expect(details().props.accessibilityState.expanded).toBe(false);
	expect(text()).not.toContain("Paused sessions");
});

it("copies only compact support diagnostics after an explicit press", async () => {
	await mount();
	await act(async () => details().props.onPress());
	expect(clipboard).not.toHaveBeenCalled();
	await act(async () => button("Copy support info").props.onPress());
	expect(clipboard).toHaveBeenCalledExactlyOnceWith('{"status":409,"code":"SANDBOX_QUOTA_EXCEEDED","requestId":"support-reference"}');
	expect(button("Support info copied")).toBeDefined();
	expect(text()).not.toContain("support-reference");
});

it("can copy useful support diagnostics when the response has no request ID", async () => {
	await mount(failure(""));
	await act(async () => details().props.onPress());
	await act(async () => button("Copy support info").props.onPress());
	expect(clipboard).toHaveBeenCalledExactlyOnceWith('{"status":409,"code":"SANDBOX_QUOTA_EXCEEDED"}');
});

it("reports a copy failure in human language and clears it after a successful retry", async () => {
	clipboard.mockRejectedValueOnce(new Error("Clipboard denied https://private.service/limit"));
	await mount();
	await act(async () => details().props.onPress());
	await act(async () => button("Copy support info").props.onPress());
	expect(text()).toContain("Couldn’t copy support info. Try again.");
	expect(text()).not.toContain("select and copy");
	expect(text()).not.toContain("private.service");
	await act(async () => button("Copy support info").props.onPress());
	expect(button("Support info copied")).toBeDefined();
	expect(text()).not.toContain("Couldn’t copy");
});

it("does not keep a copied confirmation when a later copy fails", async () => {
	await mount();
	await act(async () => details().props.onPress());
	await act(async () => button("Copy support info").props.onPress());
	clipboard.mockRejectedValueOnce(new Error("Clipboard denied"));
	await act(async () => button("Support info copied").props.onPress());
	expect(button("Copy support info")).toBeDefined();
	expect(renderer!.root.findAllByProps({ title: "Support info copied" })).toHaveLength(0);
	expect(text()).toContain("Couldn’t copy support info. Try again.");
});

it("never retries automatically and keeps the draft recovery action explicit", async () => {
	const onRetry = vi.fn();
	await act(async () => { renderer = create(<CloudSessionLimitNotice error={failure()} onRetry={onRetry} busy={false} />); });
	expect(onRetry).not.toHaveBeenCalled();
	await act(async () => button("Try again").props.onPress());
	expect(onRetry).toHaveBeenCalledOnce();
	await act(async () => renderer!.update(<CloudSessionLimitNotice error={failure()} onRetry={onRetry} busy />));
	expect(button("Try again").props.disabled).toBe(true);
	await act(async () => button("Try again").props.onPress());
	expect(onRetry).toHaveBeenCalledOnce();
});

it("closes Details and resets copy feedback when a newer error arrives", async () => {
	await mount(failure("old-reference"));
	await act(async () => details().props.onPress());
	await act(async () => button("Copy support info").props.onPress());
	await act(async () => renderer!.update(<CloudSessionLimitNotice error={failure("new-reference")} onRetry={vi.fn()} busy={false} />));
	expect(details().props.accessibilityState.expanded).toBe(false);
	await act(async () => details().props.onPress());
	expect(button("Copy support info")).toBeDefined();
	await act(async () => button("Copy support info").props.onPress());
	expect(clipboard).toHaveBeenLastCalledWith('{"status":409,"code":"SANDBOX_QUOTA_EXCEEDED","requestId":"new-reference"}');
});

it.each(["success", "failure"])("ignores an old copy %s after a newer error arrives", async (outcome) => {
	let resolve!: () => void;
	let reject!: (reason: Error) => void;
	clipboard.mockImplementationOnce(() => new Promise<void>((done, fail) => { resolve = done; reject = fail; }));
	await mount(failure("old-reference"));
	await act(async () => details().props.onPress());
	await act(async () => button("Copy support info").props.onPress());
	await act(async () => renderer!.update(<CloudSessionLimitNotice error={failure("new-reference")} onRetry={vi.fn()} busy={false} />));
	await act(async () => details().props.onPress());
	await act(async () => { if (outcome === "success") resolve(); else reject(new Error("Old clipboard failure")); });
	expect(button("Copy support info")).toBeDefined();
	expect(text()).not.toContain("Couldn’t copy");
});

it.each(["success", "failure"])("ignores a pending copy %s after dismissal", async (outcome) => {
	let resolve!: () => void;
	let reject!: (reason: Error) => void;
	clipboard.mockImplementationOnce(() => new Promise<void>((done, fail) => { resolve = done; reject = fail; }));
	await mount();
	await act(async () => details().props.onPress());
	await act(async () => button("Copy support info").props.onPress());
	await act(async () => renderer!.unmount());
	await act(async () => { if (outcome === "success") resolve(); else reject(new Error("Dismissed clipboard failure")); });
	expect(renderer!.toJSON()).toBeNull();
	renderer = undefined;
});
