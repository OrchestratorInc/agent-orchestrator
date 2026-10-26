import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";
import type { EnvironmentMachineSummary } from "./EnvironmentMachineRow.types";
import type { Host } from "./hosts";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { theme } = vi.hoisted(() => ({
	theme: { accent: "accent", bgBase: "base", bgElevated: "card", green: "green", red: "red", textPrimary: "ink", textSecondary: "gray", textTertiary: "dim", highlight: "caret" },
}));

vi.mock("@expo/ui", () => ({ Host: "Host" }));
vi.mock("@expo/ui/swift-ui", async () => (await import("./nativeFormTestDoubles")).swiftUIDouble());
vi.mock("@expo/ui/swift-ui/modifiers", async () => {
	const doubles = await import("./nativeFormTestDoubles");
	return doubles.modifierDouble(doubles.swiftUIModifierNames);
});
vi.mock("@expo/ui/jetpack-compose", async () => (await import("./nativeFormTestDoubles")).composeDouble());
vi.mock("@expo/ui/jetpack-compose/modifiers", async () => (await import("./nativeFormTestDoubles")).composeModifierDouble());
vi.mock("./machine-menu-image.android", () => ({ machineMenuImage: (symbol: string) => `${symbol}.xml` }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => theme,
	useThemeState: () => ({ scheme: "dark", theme }),
}));

import { EnvironmentMachineList } from "./EnvironmentMachineRow";

const studio: Host = {
	id: "host-1",
	name: "Studio Mac",
	platform: "darwin",
	token: "secret",
	lastConnected: 1,
	appearance: { symbol: "laptop" },
	endpoints: [
		{ kind: "lan", host: "192.168.1.24", port: 3011, secure: false },
		{ kind: "tailscale", host: "studio.tail123.ts.net", port: 3011, secure: true },
	],
};
const activeConfig: ServerConfig = { host: "studio.tail123.ts.net", httpPort: "3011", muxPort: "3012", secure: true, password: "secret", hostId: "host-1", endpointKind: "tailscale" };
const dev: Host = { ...studio, id: "host-2", name: "Dev", appearance: { symbol: "server" }, endpoints: [{ kind: "lan", host: "192.168.1.25", port: 3011, secure: false }] };

const machines: EnvironmentMachineSummary[] = [
	{ host: studio, activeConfig, isDefault: true, status: { kind: "connected", label: "Connected" } },
	{ host: dev, isDefault: false, status: { kind: "offline", label: "Offline" } },
];

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

type Modifier = { $type: string; args: unknown[] };
const modifiersOf = (node: ReactTestInstance) => (node.props.modifiers ?? []) as Modifier[];
const hostNode = (testID: string) => renderer!.root.find((node) => typeof node.type === "string" && (node.props.testID === testID || modifiersOf(node).some((m) => m.$type === "testID" && m.args[0] === testID)));
const textOf = (node: ReactTestInstance) => node.findAllByType("Text" as never).map((text) => text.props.children);

describe("Environments list on iOS", () => {
	function render(overrides: Partial<React.ComponentProps<typeof EnvironmentMachineList>> = {}) {
		const onOpen = vi.fn();
		const onRefresh = vi.fn(async () => {});
		act(() => {
			renderer = create(<EnvironmentMachineList machines={machines} footer="Machines available." refreshing={false} onRefresh={onRefresh} onOpen={onOpen} {...overrides} />);
		});
		return { onOpen, onRefresh };
	}

	it("lists each machine as a native form row with its route and status in words", () => {
		render();
		const form = renderer!.root.findByType("Form" as never);
		const section = form.findByType("Section" as never);
		const rows = section.findAllByType("Button" as never);
		expect(rows).toHaveLength(2);
		expect(textOf(rows[0])).toEqual(["Studio Mac", "Default · Tailscale · studio.tail123.ts.net", "Connected"]);
		expect(textOf(rows[1])).toEqual(["Dev", "LAN · 192.168.1.25", "Offline"]);
		// The glyph is the machine's own SF Symbol, and the row discloses a page.
		expect(rows[0].findAllByType("Image" as never).map((image) => image.props.systemName)).toEqual(["laptopcomputer", "chevron.forward"]);
		expect(modifiersOf(hostNode("environment-status-host-2"))).toContainEqual({ $type: "foregroundStyle", args: ["red"] });
		expect(modifiersOf(rows[0])).toContainEqual({ $type: "accessibilityLabel", args: ["Studio Mac, Connected, Default · Tailscale · studio.tail123.ts.net"] });
		expect(section.props.footer.props.children).toBe("Machines available.");
	});

	it("opens a machine's page from its row and refreshes from the form", async () => {
		const { onOpen, onRefresh } = render();
		act(() => hostNode("environment-machine-host-2").props.onPress());
		expect(onOpen).toHaveBeenCalledExactlyOnceWith("host-2");
		const refreshable = modifiersOf(renderer!.root.findByType("Form" as never)).find((modifier) => modifier.$type === "refreshable");
		await (refreshable?.args[0] as () => Promise<void>)();
		expect(onRefresh).toHaveBeenCalledOnce();
	});

	it("says a load failure under the list in the error colour", () => {
		render({ error: "The saved environments couldn't be loaded." });
		const footer = renderer!.root.findByType("Section" as never).props.footer;
		expect(footer.props).toMatchObject({ error: true, children: "The saved environments couldn't be loaded." });
	});

	it("has no swipe, accordion or inline editor of its own", () => {
		render();
		expect(renderer!.root.findAll((node) => /Swipeable|TextInput|Switch/.test(String(node.type)))).toHaveLength(0);
	});
});

describe("Environments list on Android", () => {
	async function render() {
		const { EnvironmentMachineList: AndroidList } = await import("./EnvironmentMachineRow.android");
		const onOpen = vi.fn();
		const onRefresh = vi.fn(async () => {});
		act(() => {
			renderer = create(<AndroidList machines={machines} footer="Machines available." refreshing onRefresh={onRefresh} onOpen={onOpen} />);
		});
		return { onOpen, onRefresh };
	}

	it("draws Material list items in the theme's surfaces inside a pull-to-refresh box", async () => {
		const { onRefresh } = await render();
		const box = renderer!.root.findByType("PullToRefreshBox" as never);
		expect(box.props.isRefreshing).toBe(true);
		expect(box.props.indicator).toEqual({ color: "accent", containerColor: "card" });
		box.props.onRefresh();
		expect(onRefresh).toHaveBeenCalledOnce();
		const rows = renderer!.root.findAllByType("ListItem" as never);
		expect(rows).toHaveLength(2);
		expect(rows[0].props.colors).toMatchObject({ containerColor: "card", contentColor: "ink", supportingContentColor: "gray" });
		expect(rows[0].findByType("ListItem.HeadlineContent" as never).findByType("Text" as never).props.children).toBe("Studio Mac");
		expect(rows[0].findByType("ListItem.SupportingContent" as never).findByType("Text" as never).props.children).toBe("Default · Tailscale · studio.tail123.ts.net");
		expect(rows[0].findByType("Icon" as never).props.source).toBe("laptop.xml");
		const status = rows[1].findByType("ListItem.TrailingContent" as never).findByType("Text" as never);
		expect(status.props).toMatchObject({ color: "red", children: "Offline" });
		// The two rows join into one group: rounded at its ends only.
		const shape = (row: ReactTestInstance) => (modifiersOf(row).find((m) => m.$type === "clip")?.args[0] as { radii: Record<string, number> }).radii;
		expect(shape(rows[0])).toEqual({ topStart: 20, topEnd: 20, bottomStart: 4, bottomEnd: 4 });
		expect(shape(rows[1])).toEqual({ topStart: 4, topEnd: 4, bottomStart: 20, bottomEnd: 20 });
	});

	it("opens a machine's page from its row with a ripple", async () => {
		const { onOpen } = await render();
		const row = hostNode("environment-machine-host-1");
		const click = modifiersOf(row).find((modifier) => modifier.$type === "clickable");
		(click?.args[0] as () => void)();
		expect(onOpen).toHaveBeenCalledExactlyOnceWith("host-1");
	});
});
