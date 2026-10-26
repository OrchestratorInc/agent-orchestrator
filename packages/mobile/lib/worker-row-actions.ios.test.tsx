import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

vi.mock("@expo/ui", () => ({ Host: "NativeHost" }));
vi.mock("@expo/ui/swift-ui", () => ({ Button: "NativeButton", HStack: "NativeStack", Image: "NativeImage" }));
vi.mock("@expo/ui/swift-ui/modifiers", () => Object.fromEntries([
	"accessibilityIdentifier", "accessibilityLabel", "buttonBorderShape", "buttonStyle", "controlSize", "frame", "labelStyle", "tint",
].map((name) => [name, (value: unknown) => ({ [name]: value })])));
vi.mock("./ThemeProvider", () => ({ useThemeState: () => ({ scheme: "dark" }), useTheme: () => ({ textPrimary: "white", red: "red" }) }));

import { WorkerRowActions } from "./worker-row-actions.ios";

function buttons() {
	let renderer!: ReactTestRenderer;
	act(() => { renderer = create(<WorkerRowActions title="Task" pinned={false} onSetPinned={() => {}} onDelete={() => {}} />); });
	const byId = (id: string) => renderer.root.findAllByType("NativeButton" as never)
		.find((button) => button.props.modifiers.some((modifier: Record<string, unknown>) => modifier.accessibilityIdentifier === id))!;
	return { pin: byId("worker-pin"), archive: byId("worker-archive") };
}

it("gives Archive the system's destructive role and red", () => {
	const { archive } = buttons();
	expect(archive.props.role).toBe("destructive");
	expect(archive.props.modifiers).toContainEqual({ tint: "red" });
	expect(archive.findByType("NativeImage" as never).props).toMatchObject({ systemName: "archivebox", color: "red" });
});

it("keeps Pin in foreground ink", () => {
	const { pin } = buttons();
	expect(pin.props.role).toBeUndefined();
	expect(pin.props.modifiers).toContainEqual({ tint: "white" });
});

it("sizes both controls to the touch target", () => {
	const { pin, archive } = buttons();
	for (const button of [pin, archive]) expect(button.props.modifiers).toContainEqual({ frame: { width: 44, height: 44 } });
});
