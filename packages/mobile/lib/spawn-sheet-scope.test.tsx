import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SpawnComposerControlsProps } from "./spawn-composer-controls.types";

const CONFIG = { hostId: "host-a" };

const fixtures = vi.hoisted(() => ({
	controls: undefined as unknown,
	projectAgents: {} as Record<string, string>,
	app: {} as Record<string, unknown>,
	spawn: vi.fn(),
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("expo-router", () => ({
	useLocalSearchParams: () => ({}),
	useRouter: () => ({ back: vi.fn(), canGoBack: () => true, push: vi.fn(), replace: vi.fn() }),
}));
vi.mock("expo-document-picker", () => ({ getDocumentAsync: vi.fn() }));
vi.mock("expo-image-picker", () => ({ launchImageLibraryAsync: vi.fn(), UIImagePickerPreferredAssetRepresentationMode: {} }));
vi.mock("expo-file-system", () => ({ File: class File {} }));
vi.mock("@expo/ui/community/bottom-sheet", () => ({ default: "BottomSheet", BottomSheetView: "BottomSheetView" }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) =>
		React.createElement(name, props, children);
	return {
		InteractionManager: { runAfterInteractions: vi.fn() },
		Platform: { OS: "ios", select: (options: { ios?: unknown }) => options.ios },
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("react-native-keyboard-controller", () => ({
	KeyboardStickyView: "KeyboardStickyView",
	useKeyboardState: (select: (state: { height: number }) => number) => select({ height: 0 }),
}));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./api", () => ({
	ApiError: class ApiError extends Error {},
	getAgents: vi.fn(async () => {
		const agents = [
			{ id: "claude-code", label: "Claude Code" },
			{ id: "codex", label: "Codex" },
			{ id: "cursor", label: "Cursor" },
		];
		return { supported: agents, installed: agents, authorized: agents };
	}),
	getSettings: vi.fn(async () => ({ chatHarnesses: ["claude-code", "codex", "cursor"] })),
	getProject: vi.fn(async (_config: unknown, id: string) => ({ id, agent: fixtures.projectAgents[id] })),
	getAgentModels: vi.fn(async (_config: unknown, agentId: string) => ({
		agentId,
		selectionMode: "catalog",
		allowCustom: false,
		models: [{ id: `${agentId}-large`, label: `${agentId} large` }],
	})),
}));
vi.mock("./haptics", () => ({ haptics: { error: vi.fn(), success: vi.fn() } }));
vi.mock("./RouteErrorBoundary", () => ({ SheetErrorBoundary: () => null }));
vi.mock("./spawn-attachment-row", () => ({ SpawnAttachmentRow: () => null }));
vi.mock("./spawn-prompt-input", () => ({ SpawnPromptInput: () => null }));
vi.mock("./spawn-composer-controls", () => ({
	SpawnComposerControls: (props: unknown) => {
		fixtures.controls = props;
		return null;
	},
}));
vi.mock("./store", () => ({ useApp: () => fixtures.app }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (make: (theme: unknown) => unknown) => make(fixtures.theme),
}));
vi.mock("./ui", () => ({ Button: "Button" }));
vi.mock("./voice/useVoiceInput", () => ({ useVoiceInput: () => ({ state: "idle", partial: "", mode: "dictate" }) }));

import SpawnModal from "../app/spawn";
import { STANDALONE_PROJECT } from "./projectFilter";

let renderer: ReactTestRenderer | undefined;

const controls = () => fixtures.controls as SpawnComposerControlsProps;
const listed = (...ids: string[]) => ids.map((id) => ({ id, name: id }));

function appWith(projects: { id: string; name: string }[]) {
	return {
		hostStates: [{ hostId: "host-a", name: "Alpha", connection: "open", config: CONFIG, projects, projectsKnown: true, loading: false }],
		activeProjectId: "ao",
		currentHostId: "host-a",
		configForHost: () => CONFIG,
		switchHost: vi.fn(),
		setActiveProject: vi.fn(),
		spawn: fixtures.spawn,
	};
}

async function settled(change: () => void = () => {}) {
	await act(async () => {
		change();
		await new Promise((resolve) => setTimeout(resolve, 0));
	});
}

async function openSheet(projects = listed("ao", "scratch")) {
	fixtures.app = appWith(projects);
	await settled(() => {
		renderer = create(<SpawnModal />);
	});
}

async function startTask() {
	await settled(() => controls().onSpawn());
	return fixtures.spawn.mock.calls.at(-1)?.[0] as { projectId?: string; harness?: string; model?: string };
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	fixtures.projectAgents = {};
	fixtures.spawn.mockReset();
	fixtures.spawn.mockResolvedValue({ id: "session-1", projectId: "ao" });
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("spawn sheet scope", () => {
	it("keeps the picked agent when Standalone is chosen", async () => {
		await openSheet();
		expect(controls().harness).toBe("claude-code");

		await settled(() => controls().onSelectHarness("codex"));
		await settled(() => controls().onSelectProject(STANDALONE_PROJECT));

		expect(controls().projectId).toBe(STANDALONE_PROJECT);
		expect(controls().harness).toBe("codex");
	});

	it("starts the task with the picked agent and model after toggling Standalone off and on", async () => {
		await openSheet();
		await settled(() => controls().onSelectHarness("codex"));
		await settled(() => controls().onSelectModel("codex-large"));

		await settled(() => controls().onSelectProject(STANDALONE_PROJECT));
		expect(controls().modelSelection).toBe("codex-large");
		await settled(() => controls().onSelectProject("scratch"));
		await settled(() => controls().onSelectProject(STANDALONE_PROJECT));

		expect(await startTask()).toMatchObject({ projectId: STANDALONE_PROJECT, harness: "codex", model: "codex-large" });
	});

	it("keeps a picked agent over the default of the project chosen next", async () => {
		fixtures.projectAgents = { scratch: "cursor" };
		await openSheet();
		await settled(() => controls().onSelectHarness("codex"));

		await settled(() => controls().onSelectProject("scratch"));

		expect(controls().harness).toBe("codex");
	});

	// The default agent can differ between scopes, and a model is only valid for
	// the agent it was picked under, so the pick holds that agent as well.
	it("keeps the agent a model was picked under when only the model was picked", async () => {
		fixtures.projectAgents = { ao: "codex" };
		await openSheet();
		expect(controls().harness).toBe("codex");
		await settled(() => controls().onSelectModel("codex-large"));

		await settled(() => controls().onSelectProject(STANDALONE_PROJECT));

		expect(await startTask()).toMatchObject({ harness: "codex", model: "codex-large" });
	});

	it("still follows each scope's default when nothing was picked", async () => {
		fixtures.projectAgents = { ao: "codex", scratch: "cursor" };
		await openSheet();
		expect(controls().harness).toBe("codex");

		await settled(() => controls().onSelectProject(STANDALONE_PROJECT));
		expect(controls().harness).toBe("claude-code");

		await settled(() => controls().onSelectProject("scratch"));
		expect(controls().harness).toBe("cursor");
		expect(await startTask()).toMatchObject({ projectId: "scratch", harness: "cursor", model: undefined });
	});

	// The sheet moved itself here, onto a project nothing was picked for (#5058).
	it("does not carry picks onto the project the sheet re-seeds to after a delete", async () => {
		fixtures.projectAgents = { scratch: "cursor" };
		await openSheet();
		expect(controls().projectId).toBe("ao");
		await settled(() => controls().onSelectHarness("codex"));
		await settled(() => controls().onSelectModel("codex-large"));

		fixtures.app = appWith(listed("scratch"));
		await settled(() => renderer?.update(<SpawnModal />));

		expect(controls().projectId).toBe("scratch");
		expect(await startTask()).toMatchObject({ projectId: "scratch", harness: "cursor", model: undefined });
	});
});
