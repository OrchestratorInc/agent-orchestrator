import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { AccountPickerState } from "./chat/chatSheetRegistry";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

vi.mock("react-native", () => ({ Platform: { OS: "ios" } }));
vi.mock("expo-router", () => ({ useRouter: () => ({ back: vi.fn() }), useLocalSearchParams: () => ({ sheetKey: lab.key }) }));
vi.mock("./backNavigation", () => ({ backOr: vi.fn() }));
vi.mock("./haptics", () => ({ haptics: { select: vi.fn() } }));
vi.mock("./chat/useSheetEntryPresent", () => ({ useSheetEntryPresent: () => {} }));
vi.mock("./RouteErrorBoundary", () => ({ SheetErrorBoundary: () => null }));
vi.mock("./ui", () => ({ SheetHeader: () => null }));
vi.mock("./NativePickerList", async () => {
	const React = await import("react");
	return { NativePickerList: (props: { children?: ReactNode }) => React.createElement("NativePickerList", props) };
});
const lab = vi.hoisted(() => ({ key: "" }));

import { parkChatSheet, resetChatSheets } from "./chat/chatSheetRegistry";
import AccountSheetRoute from "../app/sheets/account";

let renderer: ReactTestRenderer | undefined;
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; resetChatSheets(); });

describe("account sheet", () => {
	it("follows the accounts while it is up, and stops following once closed", () => {
		let publish: ((state: AccountPickerState) => void) | undefined;
		const onSelect = vi.fn();
		lab.key = parkChatSheet({
			kind: "account-picker",
			subtitle: "Claude Code on Lab Mac",
			options: [{ id: "a", title: "a@x.dev", detail: "Session (5h) 42%" }, { id: "b", title: "b@x.dev" }],
			selected: "a",
			subscribe: (listener) => { publish = listener; return () => { publish = undefined; }; },
			onSelect,
		});
		act(() => { renderer = create(<AccountSheetRoute />); });
		const list = () => renderer!.root.findByType("NativePickerList" as never);
		expect(list().props.options[0].detail).toBe("Session (5h) 42%");
		// The reload the sheet started lands: usage moved and "b" signed out
		// (drawn disabled; NativePickerList takes no tap on a disabled row).
		act(() => publish!({ options: [{ id: "a", title: "a@x.dev", detail: "Session (5h) 61%" }, { id: "b", title: "b@x.dev", detail: "Signed out", disabled: true }], selected: "a" }));
		expect(list().props.options[0].detail).toBe("Session (5h) 61%");
		expect(list().props.options[1].disabled).toBe(true);
		// The pick follows too: the account picked now is the one that moves nothing.
		act(() => publish!({ options: [{ id: "a", title: "a@x.dev" }, { id: "c", title: "c@x.dev" }], selected: "c" }));
		expect(list().props.selected).toBe("c");
		act(() => list().props.onSelect("c"));
		expect(onSelect).not.toHaveBeenCalled();
		act(() => list().props.onSelect("a"));
		expect(onSelect).toHaveBeenCalledWith("a");
		act(() => renderer!.unmount());
		renderer = undefined;
		expect(publish).toBeUndefined();
	});
});
