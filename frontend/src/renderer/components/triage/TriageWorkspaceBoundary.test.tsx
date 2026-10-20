import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import type { DaemonStatus } from "../../../shared/daemon-status";
import { TriageWorkspaceBoundary } from "./TriageWorkspaceBoundary";

const mock = vi.hoisted(() => ({ getStatus: vi.fn(), onStatus: vi.fn(), listener: undefined as ((status: DaemonStatus) => void) | undefined }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { daemon: mock } }));
vi.mock("./TriageWorkspaceView", () => ({ TriageWorkspaceView: ({ runId, recovery }: { runId?: string; recovery: boolean }) => <div data-testid="recovery" data-run={runId} data-recovery={recovery} /> }));

beforeEach(() => {
	mock.onStatus.mockImplementation((listener) => { mock.listener = listener; return () => { mock.listener = undefined; }; });
});

it("opens a healthy support conversation inside the normal workspace chrome", async () => {
	mock.getStatus.mockResolvedValue({ state: "ready", port: 3001 });
	render(<TriageWorkspaceBoundary runId="support-1"><div>Normal workspace and Agents sidebar</div></TriageWorkspaceBoundary>);
	await screen.findByText("Normal workspace and Agents sidebar");
	expect(screen.queryByTestId("recovery")).toBeNull();
});

it("keeps the same run available in recovery when the daemon fails and returns it to the workspace", async () => {
	mock.getStatus.mockResolvedValue({ state: "error", code: "spawn_failed" });
	render(<TriageWorkspaceBoundary runId="support-1"><div>Normal workspace and Agents sidebar</div></TriageWorkspaceBoundary>);
	await waitFor(() => expect(screen.getByTestId("recovery")).toHaveAttribute("data-run", "support-1"));
	expect(screen.queryByText("Normal workspace and Agents sidebar")).toBeNull();
	act(() => mock.listener?.({ state: "ready", port: 3001 }));
	await screen.findByText("Normal workspace and Agents sidebar");
	act(() => mock.listener?.({ state: "error", code: "exited" }));
	expect(screen.getByTestId("recovery")).toHaveAttribute("data-run", "support-1");
});
