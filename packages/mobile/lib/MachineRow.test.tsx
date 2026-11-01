import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./SettingsForm", () => ({ SettingsStatusRow: "SettingsStatusRow" }));

import { MachineRow, machineStatusTone } from "./MachineRow";

let renderer: ReactTestRenderer | undefined;

function row(overrides: Partial<React.ComponentProps<typeof MachineRow>> = {}) {
	act(() => {
		renderer = create(<MachineRow host={{ id: "dev", name: "Dev" }} status={{ label: "Connected", tone: "good" }} isDefault={false} onPress={vi.fn()} {...overrides} />);
	});
	return renderer!.root.findByType("SettingsStatusRow" as never);
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("MachineRow", () => {
	it("names the machine and its connection once, and opens its page", () => {
		const onPress = vi.fn();
		const status = row({ onPress });
		expect(status.props).toMatchObject({ label: "Dev", status: "Connected", tone: "good", testID: "settings-machine-dev" });
		status.props.onPress();
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("marks the default beside its connection", () => {
		expect(row({ isDefault: true }).props.status).toBe("Connected · Default");
	});

	// Most machines are someone's laptop; one given a shape draws that shape.
	it("draws the machine's own shape", () => {
		expect(row().props.icon).toBe("laptop");
		act(() => renderer?.unmount());
		expect(row({ host: { id: "box", name: "Box", appearance: { symbol: "server" } } }).props.icon).toBe("server");
	});
});

describe("machineStatusTone", () => {
	it.each([["ok", "good"], ["error", "bad"], ["neutral", "default"]] as const)("draws a %s connection in the %s tone", (status, tone) => {
		expect(machineStatusTone(status)).toBe(tone);
	});
});
