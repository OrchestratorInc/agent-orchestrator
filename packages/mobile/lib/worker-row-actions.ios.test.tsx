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

it("gives Archive the system's destructive role", () => {
	const { archive } = buttons();
	expect(archive.props.role).toBe("destructive");
});

it("keeps Pin non-destructive", () => {
	const { pin } = buttons();
	expect(pin.props.role).toBeUndefined();
});
