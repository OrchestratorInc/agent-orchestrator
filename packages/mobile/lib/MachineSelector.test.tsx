import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { haptics } = vi.hoisted(() => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));

vi.mock("./haptics", () => ({ haptics }));
vi.mock("./SettingsForm", () => ({ SettingsPickerRow: "SettingsPickerRow", SettingsValueRow: "SettingsValueRow" }));

import { MachineSelector, MachineStatusRow } from "./MachineSelector";

const hosts = [
	{ id: "prod", name: "Prod" },
	{ id: "dev", name: "Dev" },
] as never;

let renderer: ReactTestRenderer | undefined;

function render(element: React.ReactElement) {
	act(() => { renderer = create(element); });
	return renderer!;
}

function selector(overrides: Partial<React.ComponentProps<typeof MachineSelector>> = {}) {
	return render(<MachineSelector hosts={hosts} selectedHostId="dev" switchingHostId={null} onSelect={vi.fn()} {...overrides} />);
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	haptics.select.mockClear();
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

describe("MachineSelector", () => {
	// The platform's picker: a menu on iOS, a dropdown on Android, with every
	// paired machine as a choice and the default as its value.
	it("is a Default machine picker over the paired machines", () => {
		const picker = selector().root.findByType("SettingsPickerRow" as never);

		expect(picker.props).toMatchObject({ icon: "defaultMachine", label: "Default machine", selectedValue: "dev", disabled: false, testID: "machine-selector" });
		expect(picker.props.options).toEqual([{ value: "prod", label: "Prod" }, { value: "dev", label: "Dev" }]);
	});

	it("makes a newly chosen machine the default, once", () => {
		const onSelect = vi.fn();
		const picker = selector({ onSelect }).root.findByType("SettingsPickerRow" as never);

		act(() => picker.props.onValueChange("prod"));
		expect(onSelect).toHaveBeenCalledWith("prod");
		expect(haptics.select).toHaveBeenCalledOnce();

		act(() => picker.props.onValueChange("dev"));
		expect(onSelect).toHaveBeenCalledOnce();
	});

	it("holds the picker while a switch is under way", () => {
		expect(selector({ switchingHostId: "prod" }).root.findByType("SettingsPickerRow" as never).props.disabled).toBe(true);
	});

	// The saved config can name a machine before any paired one is chosen.
	it("shows the configured machine until a paired one is the default", () => {
		const onSelect = vi.fn();
		const picker = selector({ selectedHostId: null, selectedHostName: "Legacy", onSelect }).root.findByType("SettingsPickerRow" as never);

		expect(picker.props.selectedValue).toBe("");
		expect(picker.props.options[0]).toEqual({ value: "", label: "Legacy" });
		act(() => picker.props.onValueChange(""));
		expect(onSelect).not.toHaveBeenCalled();
	});

	it("reads as a plain value with no machines to choose from", () => {
		const root = selector({ hosts: [] as never, selectedHostId: null, selectedHostName: null }).root;

		expect(root.findAllByType("SettingsPickerRow" as never)).toHaveLength(0);
		expect(root.findByType("SettingsValueRow" as never).props).toMatchObject({ label: "Default machine", value: "None" });
	});

	// Choosing is the whole job: no edit, forget or add actions in here.
	it("offers nothing but the choice of default", () => {
		const root = selector().root;
		expect(root.findAll((node) => typeof node.props.onPress === "function")).toHaveLength(0);
	});
});

describe("MachineStatusRow", () => {
	it.each([
		["ok", "Connected", "good"],
		["error", "Unreachable", "bad"],
		["neutral", "Connecting…", "default"],
	] as const)("names a %s connection in words, with the matching tone", (statusTone, statusLabel, tone) => {
		const row = render(<MachineStatusRow hosts={hosts} statusLabel={statusLabel} statusTone={statusTone} switchingHostId={null} />).root.findByType("SettingsValueRow" as never);
		expect(row.props).toMatchObject({ icon: "status", label: "Status", value: statusLabel, tone });
	});

	it("says which machine it is switching to", () => {
		const row = render(<MachineStatusRow hosts={hosts} statusLabel="Connected" statusTone="ok" switchingHostId="prod" />).root.findByType("SettingsValueRow" as never);
		expect(row.props).toMatchObject({ value: "Switching to Prod…", tone: "default" });
	});
});
