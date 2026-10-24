import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useState } from "react";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
const state = vi.hoisted(() => ({ params: {} as Record<string, unknown>, app: {} as Record<string, any>, auth: {} as Record<string, any> }));
vi.mock("expo-router", () => ({ useLocalSearchParams: () => state.params, useRouter: () => ({ navigate: vi.fn() }) }));
vi.mock("react-native", () => ({ View: "View", ActivityIndicator: "ActivityIndicator", StyleSheet: { create: (styles: unknown) => styles } }));
vi.mock("../store", () => ({ useApp: () => state.app, HostScope: ({ children }: { children: unknown }) => children }));
vi.mock("../cloud/authStore", () => ({ useCloudAuth: () => state.auth }));
vi.mock("../cloud/useCloudSignInAction", () => ({ useCloudSignInAction: () => ({ busy: false, signIn: vi.fn() }) }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}) }));
vi.mock("../ui", () => ({ Button: "Button", EmptyState: "EmptyState" }));

import { FilesScope, useFilesScope } from "./FilesScope";

let screen: ReactTestRenderer;
const workspace = { scopeKey: "lease-a", isCurrent: () => true };
beforeEach(() => {
	state.params = { sessionId: "worker", source: "cloud", sourceId: "org", scopeKey: "lease-a" };
	state.app = { sourceFor: () => ({ scopeKey: "lease-a", workspace }), configForHost: () => null, loading: false, currentHostId: "mac" };
	state.auth = { signedIn: true, busy: false, orgLoading: false, orgId: "org", retryOrgResolution: vi.fn() };
});
afterEach(() => { if (screen) act(() => screen.unmount()); });

it("does not show cached file contents after switching accounts inside the same Cloud organization", () => {
	state.app.sourceFor = () => ({ scopeKey: "lease-b", workspace: { ...workspace, scopeKey: "lease-b" } });
	act(() => { screen = create(<FilesScope><span>Private file contents</span></FilesScope>); });
	expect(screen.root.findAllByType("span")).toHaveLength(0);
	expect(screen.root.findByType("EmptyState" as never).props.title).toBe("Cloud workspace changed");
});

it("keeps a Cloud source link out of the active Local workspace while signed out", () => {
	state.app.sourceFor = () => undefined;
	state.auth.signedIn = false;
	act(() => { screen = create(<FilesScope><span>Private file contents</span></FilesScope>); });
	expect(screen.root.findAllByType("span")).toHaveLength(0);
	expect(screen.root.findByType("EmptyState" as never).props.title).toBe("Sign in to Orchestrator Cloud");
});

it("waits for cold-start authentication instead of saying Cloud files are empty", () => {
	state.app.sourceFor = () => undefined;
	state.auth.signedIn = null;
	act(() => { screen = create(<FilesScope><span>Private file contents</span></FilesScope>); });
	expect(screen.root.findAllByType("span")).toHaveLength(0);
	expect(screen.root.findAllByType("ActivityIndicator" as never)).toHaveLength(1);
});

it("opens the verified Local source independently of the active Cloud destination", () => {
	state.params = { sessionId: "worker", hostId: "mac" };
	state.app.configForHost = () => ({ hostId: "mac" });
	act(() => { screen = create(<FilesScope><span>Local files</span></FilesScope>); });
	expect(screen.root.findByType("span").props.children).toBe("Local files");
});

it("rejects a Local route when the connection resolves to another paired machine", () => {
	state.params = { sessionId: "worker", hostId: "mac" };
	state.app.configForHost = () => ({ hostId: "other" });
	act(() => { screen = create(<FilesScope><span>Wrong machine files</span></FilesScope>); });
	expect(screen.root.findAllByType("span")).toHaveLength(0);
});

function ReaderProbe() {
	const scope = useFilesScope();
	const [draft, setDraft] = useState("");
	return <input value={draft} onChange={setDraft as never} data-workspace={scope.workspace.scopeKey} data-review={scope.reviewKey} />;
}

it("changes repository readers and their review queues without carrying a selected file draft into the sibling", () => {
	const sibling = { ...workspace, scopeKey: "lease-a/shared-api" };
	const base = { ...workspace, forRepository: (name?: string) => name === "shared-api" ? sibling : workspace };
	state.app.sourceFor = () => ({ scopeKey: "lease-a", workspace: base });
	act(() => { screen = create(<FilesScope><ReaderProbe /></FilesScope>); });
	act(() => screen.root.findByType("input").props.onChange("main file selection"));
	state.params.repository = "shared-api";
	act(() => screen.update(<FilesScope><ReaderProbe /></FilesScope>));
	const reader = screen.root.findByType("input");
	expect(reader.props.value).toBe("");
	expect(reader.props["data-workspace"]).toBe("lease-a/shared-api");
	expect(reader.props["data-review"]).toBe('["cloud","lease-a","worker","shared-api"]');
});
