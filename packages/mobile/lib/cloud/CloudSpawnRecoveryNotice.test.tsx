import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ View: "View", Text: "Text", Pressable: "Pressable", StyleSheet: { create: (styles: object) => styles } }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../ui", () => ({ Button: "Button" }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemedStyles: (make: (theme: typeof darkTheme) => unknown) => make(darkTheme) };
});
import { CloudSpawnRecoveryNotice } from "./CloudSpawnRecoveryNotice";

let renderer: ReactTestRenderer | undefined;
const text = () => renderer!.root.findAllByType("Text" as never).map(node => node.props.children).join(" ");
const more = () => renderer!.root.findByProps({ accessibilityLabel: "More recovery options", accessibilityRole: "button" });
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(async () => { if (renderer) await act(async () => renderer?.unmount()); renderer = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it("keeps starting a separate request behind More and an explicit user action", async () => {
	const onNewRequest = vi.fn(); const onViewSessions = vi.fn();
	await act(async () => { renderer = create(<CloudSpawnRecoveryNotice accepted={false} busy={false} onViewSessions={onViewSessions} onNewRequest={onNewRequest} />); });
	expect(text()).toContain("Couldn’t confirm the worker started");
	expect(text()).toContain("same task settings");
	expect(renderer!.root.findAllByProps({ title: "Start a new request…" })).toHaveLength(0);
	expect(onNewRequest).not.toHaveBeenCalled(); expect(onViewSessions).not.toHaveBeenCalled();
	await act(async () => more().props.onPress());
	expect(more().props.accessibilityState.expanded).toBe(true);
	expect(onNewRequest).not.toHaveBeenCalled();
	await act(async () => renderer!.root.findByProps({ title: "Start a new request…" }).props.onPress());
	expect(onNewRequest).toHaveBeenCalledOnce();
	await act(async () => renderer!.root.findByProps({ title: "View Cloud sessions" }).props.onPress());
	expect(onViewSessions).toHaveBeenCalledOnce();
});

it("shows confirmed creation without offering abandonment or another start", async () => {
	await act(async () => { renderer = create(<CloudSpawnRecoveryNotice accepted busy={false} onViewSessions={vi.fn()} onNewRequest={vi.fn()} />); });
	expect(text()).toContain("Worker created");
	expect(text()).not.toContain("Couldn’t confirm");
	expect(renderer!.root.findAllByType("Button" as never)).toHaveLength(0);
	expect(renderer!.root.findAllByType("Pressable" as never)).toHaveLength(0);
});

it("disables recovery choices while the current request is being processed", async () => {
	await act(async () => { renderer = create(<CloudSpawnRecoveryNotice accepted={false} busy onViewSessions={vi.fn()} onNewRequest={vi.fn()} />); });
	expect(renderer!.root.findByProps({ title: "View Cloud sessions" }).props.disabled).toBe(true);
	expect(more().props.disabled).toBe(true);
});
