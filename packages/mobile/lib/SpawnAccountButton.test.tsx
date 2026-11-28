import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactElement, ReactNode } from "react";
import type { AgentAccount } from "./agentAccounts";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
type PillProps = { label: string; onPress(): void; children?: ReactElement<{ account?: AgentAccount }> };
const header = vi.hoisted(() => ({ right: null as ReactElement<PillProps> | null }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: ReactNode }) => React.createElement(name, props, children);
	return { Platform: { OS: "android" }, Pressable: host("Pressable"), View: host("View"), StyleSheet: { create: (s: object) => s, absoluteFill: {} } };
});
vi.mock("expo-router", () => ({ Stack: { Toolbar: Object.assign(() => null, { View: () => null }) } }));
vi.mock("@expo/ui", () => ({ Host: () => null }));
vi.mock("@expo/ui/swift-ui", () => ({ Circle: () => null }));
vi.mock("@expo/ui/swift-ui/modifiers", () => ({ foregroundStyle: () => ({}), frame: () => ({}) }));
vi.mock("./glass", () => ({ glassCircle: () => ({}) }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("./ThemeProvider", () => ({ useThemeState: () => ({ scheme: "light" }) }));
vi.mock("./tokens", () => ({ touchTarget: 44 }));
vi.mock("./AccountUsageAvatar", async () => {
	const React = await import("react");
	return { AccountUsageAvatar: (props: object) => React.createElement("AccountUsageAvatar", props) };
});
vi.mock("./headerPill", async () => {
	const React = await import("react");
	return {
		HEADER_PILL_SIZE: 44,
		HeaderPillButton: ({ children, ...props }: { children?: ReactNode }) => React.createElement("HeaderPillButton", props, children),
		// Stands in for the bar: what the page sets as its trailing control.
		PillHeaderRight: ({ children }: { children: ReactElement<PillProps> | null }) => { header.right = children; return null; },
	};
});

import { SpawnAccountButton } from "./SpawnAccountButton";

const maya: AgentAccount = { id: "c1", provider: "claude", email: "maya@x.dev", signedIn: true, default: true, limits: { windows: [{ id: "five_hour", label: "Session (5h)", usedPercent: 42 }] } };
const old: AgentAccount = { id: "c2", provider: "claude", email: "old@x.dev", signedIn: false, default: false };

let renderer: ReactTestRenderer | undefined;
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; header.right = null; });

function render(account: AgentAccount | undefined, accounts: AgentAccount[], onPress = vi.fn()) {
	act(() => { renderer = create(<SpawnAccountButton account={account} accounts={accounts} onPress={onPress} />); });
	return onPress;
}

describe("New task account button", () => {
	it("shows the account the task starts on, ringed by its use", () => {
		const onPress = render(maya, [maya, old]);
		expect(header.right?.props.label).toBe("Account, maya@x.dev, 42% of usage limit used");
		act(() => header.right?.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("stays when the account picked earlier is gone, without standing in another", () => {
		// A restored draft's account the machine no longer has: no account matches it.
		const onPress = render(undefined, [maya, old]);
		expect(header.right?.props.label).toBe("Account, choose an account");
		expect(header.right?.props.children?.props.account).toBeUndefined();
		act(() => header.right?.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("is not offered without a signed-in account", () => {
		render(undefined, [old]);
		expect(header.right).toBeNull();
		// A signed-out default on its own: its sheet would hold one row nobody can choose.
		render(old, [old]);
		expect(header.right).toBeNull();
		render(undefined, []);
		expect(header.right).toBeNull();
	});
});
