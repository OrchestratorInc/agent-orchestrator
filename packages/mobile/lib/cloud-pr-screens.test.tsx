import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { StrictMode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { PullRequestSource } from "./pr/source";
import type { DashboardSession } from "./api";
import type { Scoped } from "./environment/scopedBoard";
import { Alert } from "react-native";
const stable = vi.hoisted(() => ({ config: { host: "local", password: "secret" }, sessions: [] as DashboardSession[], boardSessions: [] as Scoped<DashboardSession>[] }));
const f = vi.hoisted(() => ({ cloud: undefined as unknown, params: { sessionId: "session", prUrl: "https://github.com/a/b/pull/7", prNumber: "7", source: "cloud", sourceId: "org", scopeKey: "account" }, localCalls: vi.fn(), navigate: vi.fn(), open: vi.fn(), options: vi.fn(), theme: new Proxy({}, { get: () => "#888" }) }));
vi.mock("react-native", async () => { const React = await import("react"); return { View: "View", Text: "Text", ScrollView: "ScrollView", Pressable: "Pressable", Switch: "Switch", ActivityIndicator: "ActivityIndicator", RefreshControl: "RefreshControl", Alert: { alert: vi.fn() }, StyleSheet: { create: (value: unknown) => value, hairlineWidth: 1 }, Platform: { OS: "ios" } }; });
vi.mock("expo-router", async () => { const React = await import("react"); return { useLocalSearchParams: () => f.params, useRouter: () => ({ push: f.navigate, navigate: f.navigate }), useNavigation: () => ({ setOptions: f.options }), useFocusEffect: (callback: () => void) => React.useEffect(callback, [callback]) }; });
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0 }) }));
vi.mock("@expo/vector-icons", () => ({ Feather: "Feather" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => f.theme, useThemedStyles: (factory: (t: object) => object) => factory(f.theme) }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn(), success: vi.fn() } }));
vi.mock("./openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("./pageNavigation", () => ({ useOpenPage: () => f.open }));
vi.mock("./AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("./SwipeToMerge", () => ({ SwipeToMerge: "SwipeToMerge" }));
vi.mock("./item-actions-menu", () => ({ ItemActionsMenu: "ItemActionsMenu" }));
vi.mock("./reviewer-picker", () => ({ ReviewerPicker: "ReviewerPicker" }));
vi.mock("./chat/ChatMarkdown", () => ({ ChatMarkdown: "ChatMarkdown" }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null, SheetErrorBoundary: () => null }));
vi.mock("./ui", () => ({ Button: "Button", Dot: "Dot", EmptyState: "EmptyState", ListSectionHeader: "ListSectionHeader", SheetHeader: "SheetHeader", SHEET_SCROLL_CONTENT: {} }));
vi.mock("./store", () => ({ HostScope: "HostScope", useApp: () => ({ configResolved: true, config: stable.config, currentHostId: "local", sessions: stable.sessions, sourceFor: () => ({ pullRequests: f.cloud }), scopedBoard: { sessions: stable.boardSessions } }) }));
vi.mock("./api", () => Object.fromEntries(["getAgents", "getAgentModels", "getProject", "getSession", "getSessionPR", "getSessionReviews", "requestSessionRereview", "resolveSessionReviewComment", "sendMessage", "setSessionAutoInjectCI", "setSessionAutoInjectReview", "setSessionAutoReview", "switchSessionReviewer", "cancelSessionReview", "killSessionReviewer", "mergeSessionPR", "restoreSessionReviewer", "triggerSessionReview"].map((method) => [method, (...args: unknown[]) => f.localCalls(method, ...args)])));
import ReviewDetailScreen from "../app/review/[sessionId]";
import ReviewActionsSheet from "../app/sheets/review-actions";
let renderer: ReactTestRenderer;
const pr = { url: f.params.prUrl, number: 7, title: "Cloud pull request", state: "open", repo: "a/b", author: "me", headSha: "sha", sourceBranch: "fix", targetBranch: "main", additions: 1, deletions: 0, changedFiles: 1, ci: { state: "passing", failingChecks: [] }, review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [], reviews: [] }, mergeability: { state: "mergeable", reasons: [] } };
function source(overrides: object = {}) { return { kind: "cloud", scopeKey: "account", isCurrent: () => true, list: vi.fn(async () => [pr]), reviews: vi.fn(async () => ({ reviewerHandleId: "", reviews: [], runs: [], availableReviewerHarnesses: ["codex"] })), preferences: vi.fn(async () => ({ reviewerHarness: "", workerHarness: "codex", autoReviewEnabled: false, autoInjectReview: true, autoInjectCI: true })), inspectHarnesses: vi.fn(async () => ({ harnesses: [{ harness: "codex", status: "missing" }] })), installHarness: vi.fn(async () => ({ harness: "codex", status: "ready" })), updatePreferences: vi.fn(async () => {}), trigger: vi.fn(async () => ({ reviewerHandleId: "", reviews: [], runs: [] })), ...overrides } as unknown as PullRequestSource; }
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); f.localCalls.mockReset(); vi.mocked(Alert.alert).mockClear(); stable.boardSessions = []; stable.sessions = []; f.cloud = source(); Object.assign(f.params, { sessionId: "session", source: "cloud", sourceId: "org", scopeKey: "account", prUrl: pr.url, prNumber: "7" }); delete (f.params as Record<string, unknown>).hostId; });
afterEach(() => { act(() => renderer?.unmount()); vi.unstubAllGlobals(); });
const worker: DashboardSession = { id: "session", projectId: "project", kind: "worker", displayName: "Same ID on each source", status: "idle", mode: "chat", branch: "fix", issueId: null, issueTitle: null, userPrompt: null, summary: null, createdAt: "2026-10-24T00:00:00Z", lastActivityAt: "2026-10-24T00:00:00Z" };
function feedbackPR(body: string) { return { ...pr, review: { ...pr.review, unresolvedBy: [{ reviewerId: "reviewer", count: 1, links: [{ url: `${pr.url}#discussion_r7`, body, autoInjectReview: false }] }] } }; }
it.each(["Fix the edge case", ""])('uses resolved Cloud ownership for feedback controls on an unscoped route with body %j', async (body) => {
  delete (f.params as Record<string, unknown>).source; delete (f.params as Record<string, unknown>).sourceId;
  stable.boardSessions = [{ source: { kind: "cloud", id: "org" }, value: worker }];
  // Retained Local session/config have the same id; only the scoped board owns this route.
  stable.sessions = [worker];
  const sendFeedback = vi.fn(async () => {});
  f.cloud = source({ list: async () => [feedbackPR(body)], sendFeedback });
  await act(async () => { renderer = create(<ReviewActionsSheet />); });
  const menu = renderer!.root.findByProps({ accessibilityLabel: "Actions for reviewer's comment" });
  expect(menu.props.actions.map((action: { id: string }) => action.id)).toEqual(body ? ["open", "send"] : ["open"]);
  if (body) await act(async () => { menu.props.actions.find((action: { id: string }) => action.id === "send").onPress(); });
  expect(sendFeedback).toHaveBeenCalledTimes(body ? 1 : 0);
  expect(Alert.alert).not.toHaveBeenCalled(); expect(f.localCalls).not.toHaveBeenCalled();
});
it("preserves Local comment resolution through its paired source", async () => {
  Object.assign(f.params, { source: "local", sourceId: "local", hostId: "local" });
  const localPR = feedbackPR("Fix this locally");
  const reviews = { reviewerHandleId: "", reviews: [], runs: [] };
  f.localCalls.mockImplementation((method: string) => Promise.resolve(method === "getAgents" ? { supported: [], installed: [], authorized: [] } : method === "getSessionPR" ? [localPR] : method === "getSessionReviews" ? reviews : method === "getSession" ? { ...worker, reviewerHarness: "", autoReviewEnabled: false, autoInjectReview: true, autoInjectCI: true } : undefined));
  await act(async () => { renderer = create(<ReviewActionsSheet />); });
  expect(renderer!.root.findAllByProps({ accessibilityRole: "alert" }).map((node) => node.props.children)).toEqual([]);
  expect(renderer!.root.findAllByType("Text" as never).map((node) => node.props.children)).toContain("Fix this locally");
  const menu = renderer!.root.findByProps({ accessibilityLabel: "Actions for reviewer's comment" });
  const resolve = menu.props.actions.find((action: { id: string }) => action.id === "resolve");
  expect(resolve).toBeDefined();
  act(() => resolve.onPress());
  const buttons = vi.mocked(Alert.alert).mock.calls.at(-1)![2]!;
  await act(async () => { buttons.find((button) => button.text === "Resolve")!.onPress!(); });
  expect(f.localCalls).toHaveBeenCalledWith("resolveSessionReviewComment", stable.config, "session", pr.url, `${pr.url}#discussion_r7`);
});
it.each([ReviewDetailScreen, ReviewActionsSheet])("renews the Cloud request lifetime after StrictMode effect replay", async (Screen) => {
  const cloud = source(); f.cloud = cloud;
  await act(async () => { renderer = create(<StrictMode><Screen /></StrictMode>); });
  const requests = vi.mocked(cloud.list).mock.calls;
  expect(requests.length).toBeGreaterThanOrEqual(2);
  expect(requests[0][1]?.signal?.aborted).toBe(true);
  expect(requests.at(-1)![1]?.signal?.aborted).toBe(false);
  expect(renderer!.root.findAllByType("Text" as never).some((node) => node.props.children === (Screen === ReviewDetailScreen ? "Cloud pull request" : "AUTOMATION"))).toBe(true);
  expect(f.localCalls).not.toHaveBeenCalled();
});
it.each([{ sessionId: ["session", "other"] }, { sessionId: " " }, { source: "cloud", hostId: "local" }, { scopeKey: "old-account" }, { source: "unknown" }])("rejects malformed or stale ownership before loading PRs: %o", async (params) => {
  const cloud = f.cloud as PullRequestSource;
  Object.assign(f.params, params);
  await act(async () => { renderer = create(<ReviewDetailScreen />); });
  expect(cloud.list).not.toHaveBeenCalled(); expect(cloud.reviews).not.toHaveBeenCalled(); expect(f.localCalls).not.toHaveBeenCalled();
});
it("uses Cloud automation preferences before offering a manual review", async () => {
  f.cloud = source({ preferences: async () => ({ reviewerHarness: "codex", workerHarness: "codex", autoReviewEnabled: true, autoInjectReview: true, autoInjectCI: true }) });
  await act(async () => { renderer = create(<ReviewDetailScreen />); });
  expect(renderer!.root.findByProps({ accessibilityLabel: "Start review" }).props.disabled).toBe(true);
});
it("opens only the running dedicated Cloud reviewer terminal with its full owner", async () => {
  const run = { id: "run", reviewId: "review", sessionId: "session", batchId: "batch", harness: "codex", triggerSource: "manual", prUrl: pr.url, targetSha: "sha", status: "running", verdict: "", body: "", githubReviewId: "", createdAt: "now", autoInjectReview: true, reviewerTerminalId: "123e4567-e89b-42d3-a456-426614174000" };
  f.cloud = source({ reviews: async () => ({ reviewerHandleId: run.reviewerTerminalId, reviews: [{ prUrl: pr.url, prNumber: 7, title: pr.title, targetSha: "sha", status: "running", latestRun: run }], runs: [run] }) });
  await act(async () => { renderer = create(<ReviewDetailScreen />); });
  act(() => renderer!.root.findByProps({ accessibilityLabel: "Open" }).props.onPress());
  expect(f.open).toHaveBeenCalledWith({ pathname: "/session/[id]", params: { id: "session", view: "reviewer", terminalId: run.reviewerTerminalId, source: "cloud", sourceId: "org", scopeKey: "account" } });
  expect(f.localCalls).not.toHaveBeenCalled();
});
it("renders the real Cloud PR before any AO review exists and starts review without Local calls", async () => {
  await act(async () => { renderer = create(<ReviewDetailScreen />); });
  expect(renderer!.root.findAllByType("Text" as never).some((node) => node.props.children === "Cloud pull request")).toBe(true);
  const start = renderer!.root.findByProps({ accessibilityLabel: "Start review" });
  await act(async () => { await start.props.onPress(); });
  expect((f.cloud as PullRequestSource).trigger).toHaveBeenCalledWith("session", expect.objectContaining({ signal: expect.any(AbortSignal) }));
  expect(f.localCalls).not.toHaveBeenCalled();
});
it("keeps Cloud credentials separate from binary installation and persists only a ready reviewer", async () => {
  const cloud = source(); f.cloud = cloud;
  await act(async () => { renderer = create(<ReviewActionsSheet />); });
  expect(f.localCalls).not.toHaveBeenCalled();
  const install = renderer!.root.findByProps({ accessibilityLabel: "Install Codex reviewer" });
  await act(async () => { await install.props.onPress(); });
  expect(cloud.installHarness).toHaveBeenCalledWith("session", "codex", expect.objectContaining({ signal: expect.any(AbortSignal) }));
  expect(cloud.updatePreferences).toHaveBeenCalledWith("session", { reviewerHarness: "codex" }, expect.objectContaining({ signal: expect.any(AbortSignal) }));
  expect(renderer!.root.findAllByType("ReviewerPicker" as never)).toHaveLength(0);
});
