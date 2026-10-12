import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./ThemeProvider", () => ({
	useTheme: () => new Proxy({}, { get: () => "#000000" }),
	useThemedStyles: (factory: (theme: object) => object) => factory(new Proxy({}, { get: () => "#000000" })),
}));
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));
vi.mock("./haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn() } }));
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ bottom: 0 }) }));
vi.mock("@expo/ui/community/bottom-sheet", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) =>
		React.createElement(name, props, children);
	return {
		default: host("BottomSheet"),
		BottomSheetScrollView: host("BottomSheetScrollView"),
		BottomSheetView: host("BottomSheetView"),
	};
});
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) =>
		React.createElement(name, props, children);
	return {
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});

import { WorkerControlsSheet } from "./worker-controls-sheet";
import { WorkerControlsSheet as AndroidWorkerControlsSheet } from "./worker-controls-sheet.android";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

const props = {
	open: true,
	onDismiss: vi.fn(),
	onSearch: vi.fn(),
	environments: [{ id: "mac", name: "Mac" }],
	selectedEnvironmentId: "all",
	onSelectEnvironment: vi.fn(),
	projects: [],
	selectedProjectId: "all",
	onSelectProject: vi.fn(),
};

describe.each([
	// iOS reports onClose after the dismissal finishes. Android reports it as the
	// hide starts, and focusing search then left the keyboard closed on device.
	["iOS fallback", WorkerControlsSheet, 0],
	["Android", AndroidWorkerControlsSheet, 180],
] as const)("%s worker controls", (_platform, Controls, searchDelayMs) => {
	it("shows each saved host symbol instead of a generic monitor", async () => {
		await act(async () => { renderer = create(<Controls {...props} environments={[
			{ id: "mac", name: "Mac", appearance: { symbol: "laptop" } },
			{ id: "dev", name: "Dev", appearance: { symbol: "linux" } },
		]} />); });
		const option = (label: string) => renderer!.root.findAllByType("Pressable" as never).find(node => node.findAllByType("Text" as never).some(text => text.props.children === label))!;
		expect(option("Mac").findAll(node => String(node.type) === "Feather")[0].props.name).toBe("laptop");
		expect(option("Dev").findAll(node => String(node.type) === "Feather")[0].props.name).toBe("terminal");
	});
	it("uses a server glyph for the all-environments option", async () => {
		await act(async () => { renderer = create(<Controls {...props} />); });

		const option = renderer!.root.findByProps({ testID: "worker-environment-filter" });
		const icon = option.findAll((node) => String(node.type) === "Feather")[0];
		expect(icon.props.name).toBe("server");
	});

	it("applies an environment immediately and closes without requiring another tap", async () => {
		const onSelectEnvironment = vi.fn(), onSelectProject = vi.fn(), onDismiss = vi.fn();
		const input = { ...props, onSelectEnvironment, onSelectProject, onDismiss };
		await act(async () => { renderer = create(<Controls {...input} />); });
		const mac = renderer!.root.findAllByType("Pressable" as never).find(node => node.findAllByType("Text" as never).some(text => text.props.children === "Mac"))!;
		await act(async () => { mac.props.onPress(); });
		expect(onSelectEnvironment).toHaveBeenCalledExactlyOnceWith("mac");
		expect(onDismiss).toHaveBeenCalledTimes(1);
		expect(renderer!.root.findAllByType("Pressable" as never).find(node => node.findAllByType("Text" as never).some(text => text.props.children === "Mac"))!.props.accessibilityState.selected).toBe(true);
		await act(async () => { renderer!.root.findByType("BottomSheet" as never).props.onClose(); });
		expect(onSelectEnvironment).toHaveBeenCalledExactlyOnceWith("mac");
		await act(async () => { renderer!.root.findByType("BottomSheet" as never).props.onClose(); });
		expect(onSelectEnvironment).toHaveBeenCalledTimes(1);
	});

	it("clears an incompatible project immediately when changing environments", async () => {
		const onSelectEnvironment = vi.fn(), onSelectProject = vi.fn(), onDismiss = vi.fn();
		const input = { ...props, selectedProjectId: '["dev","other"]', projects: [{ id: "ao", name: "AO", hostId: "mac" }, { id: "other", name: "Other", hostId: "dev" }], onSelectEnvironment, onSelectProject, onDismiss };
		await act(async () => { renderer = create(<Controls {...input} />); });
		const option = (label: string) => renderer!.root.findAllByType("Pressable" as never).find(node => node.findAllByType("Text" as never).some(text => text.props.children === label))!;
		await act(async () => { option("Mac").props.onPress(); });
		expect(option("Other")).toBeUndefined();
		expect(onSelectEnvironment).toHaveBeenCalledExactlyOnceWith("mac");
		expect(onSelectProject).toHaveBeenCalledExactlyOnceWith("all");
		expect(onDismiss).toHaveBeenCalledTimes(1);
		await act(async () => { renderer!.root.findByType("BottomSheet" as never).props.onClose(); });
		expect(onSelectEnvironment).toHaveBeenCalledExactlyOnceWith("mac");
		expect(onSelectProject).toHaveBeenCalledExactlyOnceWith("all");
	});

	it("applies a project immediately and closes the sheet", async () => {
		const onSelectProject = vi.fn(), onDismiss = vi.fn();
		await act(async () => { renderer = create(<Controls {...props} projects={[{ id: "ao", name: "AO", hostId: "mac" }]} selectedEnvironmentId="mac" onSelectProject={onSelectProject} onDismiss={onDismiss} />); });
		const project = renderer!.root.findAllByType("Pressable" as never).find(node => node.findAllByType("Text" as never).some(text => text.props.children === "AO"))!;
		await act(async () => { project.props.onPress(); });
		expect(onSelectProject).toHaveBeenCalledExactlyOnceWith('["mac","ao"]');
		expect(onDismiss).toHaveBeenCalledTimes(1);
		await act(async () => { renderer!.root.findByType("BottomSheet" as never).props.onClose(); });
		expect(onSelectProject).toHaveBeenCalledTimes(1);
	});

	it("closes even when the current environment is selected again", async () => {
		const onSelectEnvironment = vi.fn(), onDismiss = vi.fn();
		await act(async () => { renderer = create(<Controls {...props} selectedEnvironmentId="mac" onSelectEnvironment={onSelectEnvironment} onDismiss={onDismiss} />); });
		const mac = renderer!.root.findAllByType("Pressable" as never).find(node => node.findAllByType("Text" as never).some(text => text.props.children === "Mac"))!;
		await act(async () => { mac.props.onPress(); });
		expect(onDismiss).toHaveBeenCalledTimes(1);
		expect(onSelectEnvironment).not.toHaveBeenCalled();
	});

	it("opens search only once the sheet has finished hiding", async () => {
		vi.useFakeTimers();
		try {
			const onSearch = vi.fn();
			await act(async () => { renderer = create(<Controls {...props} onSearch={onSearch} />); });
			await act(async () => { renderer!.root.findByProps({ accessibilityLabel: "Search workers" }).props.onPress(); });
			expect(onSearch).not.toHaveBeenCalled();
			await act(async () => { renderer!.root.findByType("BottomSheet" as never).props.onClose(); });
			if (searchDelayMs > 0) {
				expect(onSearch).not.toHaveBeenCalled();
				await act(async () => { vi.advanceTimersByTime(searchDelayMs); });
			}
			expect(onSearch).toHaveBeenCalledTimes(1);
		} finally {
			vi.useRealTimers();
		}
	});

	it("starts a reopened sheet from the applied selection instead of its previous draft", async () => {
		await act(async () => { renderer = create(<Controls {...props} />); });
		const mac = () => renderer!.root.findAllByType("Pressable" as never).find(node => node.findAllByType("Text" as never).some(text => text.props.children === "Mac"))!;
		await act(async () => { mac().props.onPress(); });
		await act(async () => { renderer!.update(<Controls {...props} open={false} />); });
		await act(async () => { renderer!.root.findByType("BottomSheet" as never).props.onClose(); });
		await act(async () => { renderer!.update(<Controls {...props} open={true} selectedEnvironmentId="all" />); });
		expect(mac().props.accessibilityState.selected).toBe(false);
	});
});
