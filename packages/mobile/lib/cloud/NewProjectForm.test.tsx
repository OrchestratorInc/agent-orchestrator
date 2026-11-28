import { createElement, type ReactNode } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudGitHubRepository } from "./types";
import type { NewCloudProject } from "./useNewCloudProject";

// Renders both faces of the New cloud project sheet with the native views as
// host elements (see nativeFormTestDoubles), from a hand-made model, so each
// state's rows can be read and pressed without a native runtime.

const fixtures = vi.hoisted(() => ({ theme: new Proxy({}, { get: () => "#000000" }) }));

function host(name: string) {
	return function NativeView({ children, ...props }: { children?: ReactNode }) {
		return createElement(name, props, children);
	};
}
function slotted(name: string, slots: readonly string[]) {
	return Object.assign(host(name), Object.fromEntries(slots.map((slot) => [slot, host(`${name}.${slot}`)])));
}

vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	View: "View",
	Text: "Text",
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
}));
vi.mock("react-native-keyboard-controller", () => ({ KeyboardAvoidingView: "KeyboardAvoidingView" }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }) }));
vi.mock("expo-router", () => ({
	Stack: { Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton"), View: host("StackToolbarView") }) },
}));
vi.mock("@expo/ui", () => ({ Host: "Host" }));
vi.mock("@expo/ui/swift-ui", async () => (await import("../nativeFormTestDoubles")).swiftUIDouble());
vi.mock("@expo/ui/swift-ui/modifiers", async () => {
	const doubles = await import("../nativeFormTestDoubles");
	return doubles.modifierDouble([...doubles.swiftUIModifierNames, "accessibilityHidden", "accessibilityIdentifier", "listRowInsets"]);
});
vi.mock("@expo/ui/jetpack-compose", async () => {
	const doubles = await import("../nativeFormTestDoubles");
	return {
		...doubles.composeDouble(),
		Box: "Box",
		CircularProgressIndicator: "CircularProgressIndicator",
		DropdownMenu: slotted("DropdownMenu", ["Trigger", "Items"]),
		DropdownMenuItem: slotted("DropdownMenuItem", ["Text", "LeadingIcon", "TrailingIcon"]),
		TextField: slotted("TextField", ["Placeholder", "LeadingIcon", "TrailingIcon"]),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", async () => {
	const doubles = await import("../nativeFormTestDoubles");
	return { ...doubles.composeModifierDouble(), ...doubles.modifierDouble(["alpha", "size"]) };
});
vi.mock("../ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ scheme: "dark", theme: fixtures.theme }),
	useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme),
}));
vi.mock("../formFields", () => ({ FormTextField: "FormTextField", formModifiers: () => [], sectionModifiers: () => [] }));
vi.mock("../SettingsForm", () => ({ SettingsSection: "SettingsSection" }));
vi.mock("../InlineBanner", () => ({ InlineBanner: "InlineBanner" }));
vi.mock("../useHeaderOverlap", () => ({ useGlassBarInset: () => 0 }));
vi.mock("../Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("../AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("../ui", () => ({ Button: "Button" }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: undefined }) }));
vi.mock("../toolbarIcon.android", () => ({ materialDrawable: (name: string) => `${name}.xml` }));
vi.mock("../toolbarIcon", () => ({ toolbarIcon: (name: string) => `${name}.icon` }));
vi.mock("../harnessLogoAssets", () => ({ logoFor: (harness: string) => `${harness}.png` }));
vi.mock("../spawn-composer-controls.ios", () => ({ HarnessImage: "HarnessImage" }));
vi.mock("../files/SegmentedChoice", () => ({ SegmentedChoice: "SegmentedChoice" }));

const ios = await import("./NewProjectForm");
const android = await import("./NewProjectForm.android");

function repo(fullName: string, extra: Partial<CloudGitHubRepository> = {}): CloudGitHubRepository {
	return {
		githubRepositoryId: `id-${fullName}`,
		name: fullName.split("/")[1] ?? fullName,
		fullName,
		htmlUrl: `https://github.com/${fullName}`,
		defaultBranch: "main",
		visibility: "private",
		isPrivate: true,
		isArchived: false,
		access: "admin",
		grantedAt: "2026-01-01T00:00:00Z",
		...extra,
	};
}

function model(overrides: Partial<NewCloudProject> = {}): NewCloudProject {
	const repos = [repo("acme/api"), repo("acme/web", { isPrivate: false })];
	return {
		status: "ready",
		org: { id: "org-1", displayName: "Acme" } as NewCloudProject["org"],
		source: "github",
		chooseSource: vi.fn(),
		githubUnavailable: false,
		hasInstallation: true,
		repos,
		loadingRepos: false,
		reposFailed: false,
		retryRepos: vi.fn(),
		refreshRepos: vi.fn(async () => {}),
		connecting: false,
		connectGitHub: vi.fn(async () => {}),
		query: "",
		setQuery: vi.fn(),
		searching: false,
		matches: [],
		repo: undefined,
		chooseRepo: vi.fn(),
		changeRepo: vi.fn(),
		reselecting: false,
		name: "",
		setName: vi.fn(),
		available: ["claude-code", "codex"],
		workerAgent: "claude-code",
		chooseWorkerAgent: vi.fn(),
		orchestratorAgent: "codex",
		chooseOrchestratorAgent: vi.fn(),
		busy: false,
		error: null,
		dismissError: vi.fn(),
		canCreate: false,
		create: vi.fn(async () => {}),
		...overrides,
	};
}

let renderer: ReactTestRenderer | undefined;
const onConnectAgent = vi.fn();

function render(element: ReactNode): ReactTestInstance {
	act(() => { renderer = create(<>{element}</>); });
	return renderer!.root;
}

/** Whether a rendered node is the host element a native view double draws. */
function is(node: ReactTestInstance, name: string): boolean {
	return (node.type as unknown) === name;
}

function texts(root: ReactTestInstance): string[] {
	return root.findAll((node) => typeof node.type === "string").flatMap((node) => node.children.filter((child): child is string => typeof child === "string"));
}

function byTestID(root: ReactTestInstance, id: string): ReactTestInstance {
	return root.find((node) => typeof node.type === "string" && node.props.testID === id);
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	onConnectAgent.mockReset();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

describe("New cloud project form, iOS", () => {
	it("searches before it lists, and says how many repositories the search covers", () => {
		const root = render(<ios.NewProjectForm project={model()} onConnectAgent={onConnectAgent} />);
		expect(byTestID(root, "new-project-repo-search").props.placeholder).toBe("Search repositories");
		const section = root.findAll((node) => is(node, "SettingsSection")).find((node) => node.props.title === "Repository");
		expect(section?.props.footer).toBe("Type to find one of 2 repositories the app can reach.");
		expect(root.findAll((node) => typeof node.props.testID === "string" && node.props.testID.startsWith("new-project-repo-id-"))).toHaveLength(0);
	});

	it("lists the matches once something is typed and picks one on a tap", () => {
		const project = model({ query: "web", searching: true, matches: [repo("acme/web", { isPrivate: false })] });
		const root = render(<ios.NewProjectForm project={project} onConnectAgent={onConnectAgent} />);
		act(() => byTestID(root, "new-project-repo-id-acme/web").props.onPress());
		expect(project.chooseRepo).toHaveBeenCalledWith("id-acme/web");
		expect(texts(root)).toContain("Public · main");
	});

	it("says when nothing matches", () => {
		const root = render(<ios.NewProjectForm project={model({ query: "zzz", searching: true })} onConnectAgent={onConnectAgent} />);
		expect(texts(root)).toContain("No repository matches “zzz”.");
	});

	it("shows the chosen repository with Change, and its name as the project's", () => {
		const project = model({ repo: repo("acme/api"), canCreate: true });
		const root = render(<ios.NewProjectForm project={project} onConnectAgent={onConnectAgent} />);
		expect(() => byTestID(root, "new-project-repo-search")).toThrow();
		const change = root.find((node) => is(node, "Button") && node.props.label === "Change");
		act(() => change.props.onPress());
		expect(project.changeRepo).toHaveBeenCalled();
		expect(root.find((node) => is(node, "FormTextField")).props.placeholder).toBe("api");
	});

	it("connects GitHub when no installation can be used", () => {
		const project = model({ hasInstallation: false, repos: [] });
		const root = render(<ios.NewProjectForm project={project} onConnectAgent={onConnectAgent} />);
		act(() => byTestID(root, "new-project-connect-github").props.onPress());
		expect(project.connectGitHub).toHaveBeenCalled();
	});

	it("offers the connected agents for each role, and a way to connect one when there is none", () => {
		const root = render(<ios.NewProjectForm project={model()} onConnectAgent={onConnectAgent} />);
		const pickers = root.findAll((node) => is(node, "Picker") && node.props.label !== "Source");
		expect(pickers.map((picker) => [picker.props.label, picker.props.selection])).toEqual([["Workers run", "claude-code"], ["Orchestrator runs", "codex"]]);

		act(() => renderer?.unmount());
		const none = render(<ios.NewProjectForm project={model({ available: [], workerAgent: undefined, orchestratorAgent: undefined })} onConnectAgent={onConnectAgent} />);
		act(() => byTestID(none, "new-project-connect-agent").props.onPress());
		expect(onConnectAgent).toHaveBeenCalled();
	});

	it("drops the source choice where only standalone projects exist", () => {
		const root = render(<ios.NewProjectForm project={model({ githubUnavailable: true, source: "standalone" })} onConnectAgent={onConnectAgent} />);
		expect(root.findAll((node) => is(node, "Picker") && node.props.label === "Source")).toHaveLength(0);
		expect(root.findAll((node) => is(node, "SettingsSection") && node.props.title === "Repository")).toHaveLength(0);
	});

	it("puts Cancel and Create in the native bar, Create off until the form is ready", () => {
		const onCreate = vi.fn();
		const root = render(<ios.NewProjectBar canCreate={false} busy={false} onCancel={vi.fn()} onCreate={onCreate} />);
		const create = root.find((node) => is(node, "StackToolbarButton") && node.props.accessibilityLabel === "Create project");
		expect(create.props.disabled).toBe(true);
	});
});

describe("New cloud project form, Android", () => {
	it("puts Create in the page's top app bar as a check, and a spinner there while creating", () => {
		const onCreate = vi.fn();
		const root = render(<android.NewProjectBar canCreate busy={false} onCancel={vi.fn()} onCreate={onCreate} />);
		const create = root.find((node) => is(node, "StackToolbarButton") && node.props.accessibilityLabel === "Create project");
		expect(create.props.icon).toBe("checkmark.icon");
		expect(create.props.disabled).toBe(false);
		act(() => create.props.onPress());
		expect(onCreate).toHaveBeenCalled();
		expect(render(<android.NewProjectBar canCreate busy={false} onCancel={vi.fn()} onCreate={vi.fn()} />)
			.findAll((node) => is(node, "Spinner"))).toHaveLength(0);
		const busy = render(<android.NewProjectBar canCreate busy onCancel={vi.fn()} onCreate={vi.fn()} />);
		expect(busy.findAll((node) => is(node, "StackToolbarButton"))).toHaveLength(0);
		expect(busy.findAll((node) => is(node, "Spinner"))).toHaveLength(1);
	});

	it("lists matches under the search field and picks one on a tap", () => {
		const project = model({ query: "api", searching: true, matches: [repo("acme/api")] });
		const root = render(<android.NewProjectForm project={project} onConnectAgent={onConnectAgent} />);
		expect(root.findAll((node) => is(node, "TextField"))).toHaveLength(1);
		const row = root.find((node) => is(node, "ListItem") && node.props.modifiers?.some((modifier: { $type: string; args: unknown[] }) => modifier.$type === "testID" && modifier.args[0] === "new-project-repo-id-acme/api"));
		const click = row.props.modifiers.find((modifier: { $type: string }) => modifier.$type === "clickable");
		act(() => click.args[0]());
		expect(project.chooseRepo).toHaveBeenCalledWith("id-acme/api");
	});

	it("labels the name optional for a repository project and required for a standalone one", () => {
		const root = render(<android.NewProjectForm project={model()} onConnectAgent={onConnectAgent} />);
		expect(root.find((node) => is(node, "FormTextField")).props.label).toBe("Name (optional)");
		act(() => renderer?.unmount());
		const standalone = render(<android.NewProjectForm project={model({ source: "standalone" })} onConnectAgent={onConnectAgent} />);
		expect(standalone.find((node) => is(node, "FormTextField")).props.label).toBe("Name");
		expect(texts(standalone)).toContain("Sessions start in an empty folder; agents can create a repository later.");
	});

	it("shows an error over the form and a retry when the repositories fail to load", () => {
		const project = model({ repos: null, reposFailed: true, hasInstallation: false, error: "Couldn't reach Orchestrator Cloud." });
		const root = render(<android.NewProjectForm project={project} onConnectAgent={onConnectAgent} />);
		expect(root.find((node) => is(node, "InlineBanner")).props.title).toBe("Couldn't reach Orchestrator Cloud.");
		expect(texts(root)).toContain("Try again");
		expect(texts(root)).not.toContain("Connect GitHub");
	});

	it("offers each role's agent in a dropdown of the connected ones", () => {
		const project = model();
		const root = render(<android.NewProjectForm project={project} onConnectAgent={onConnectAgent} />);
		const items = root.findAll((node) => is(node, "DropdownMenuItem"));
		expect(items).toHaveLength(4);
		act(() => items[1]!.props.onClick());
		expect(project.chooseWorkerAgent).toHaveBeenCalledWith("codex");
	});
});
