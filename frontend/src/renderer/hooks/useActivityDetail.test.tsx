import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import type { ConversationActivity } from "../types/conversation";
import { ActivityDetailProvider, ActivityDetailStatus, useActivityDetail } from "./useActivityDetail";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: get }) }));

const row: ConversationActivity = {
	kind: "activity", id: "command:1", sequence: 2, revision: 1,
	activityKind: "command", status: "completed", summary: "Read file",
	detailOmitted: true, detail: { command: "cat file", output: "preview" }, createdAt: "2026-10-26T00:00:00Z",
};
function Body({ activity = row, visible = false }: { activity?: ConversationActivity; visible?: boolean }) {
	const loaded = useActivityDetail(activity, visible);
	return visible ? loaded.pending
		? <ActivityDetailStatus error={loaded.error} retry={loaded.retry} />
		: <pre>{loaded.activity.detail?.output ?? loaded.activity.detail?.text}</pre>
		: <span>Closed</span>;
}
function view(activity = row, visible = false, sessionId = "s1") {
	return <ActivityDetailProvider sessionId={sessionId}><Body activity={activity} visible={visible} /></ActivityDetailProvider>;
}
beforeEach(() => { get.mockReset(); });

it("reads only on expansion and caches reopen by revision", async () => {
	let resolve!: (value: unknown) => void;
	get.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
	const { rerender } = render(view());
	expect(get).not.toHaveBeenCalled();
	rerender(view(row, true));
	await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
	expect(screen.getByText("Loading activity…")).toBeInTheDocument();
	expect(screen.queryByText("preview")).not.toBeInTheDocument();
	await act(async () => { resolve({ data: { ...row, detailOmitted: undefined, detail: { output: "full output" } } }); });
	expect(await screen.findByText("full output")).toBeInTheDocument();
	rerender(view(row, false));
	rerender(view(row, true));
	expect(await screen.findByText("full output")).toBeInTheDocument();
	expect(get).toHaveBeenCalledTimes(1);
	get.mockResolvedValue({ data: { ...row, revision: 2, detailOmitted: undefined, detail: { output: "new output" } } });
	rerender(view({ ...row, revision: 2 }, true));
	expect(await screen.findByText("new output")).toBeInTheDocument();
	expect(get).toHaveBeenCalledTimes(2);
	expect(get.mock.calls[0][0]).toBe("/api/v1/sessions/{sessionId}/conversation/activities/{activityId}");
});

it("old daemon rows without omission markers and running output never fetch", () => {
	const { rerender } = render(view({ ...row, detailOmitted: undefined }, true));
	expect(screen.getByText("preview")).toBeInTheDocument();
	rerender(view({ ...row, status: "running", detailOmitted: undefined, detail: { output: "streaming" } }, true));
	expect(screen.getByText("streaming")).toBeInTheDocument();
	expect(get).not.toHaveBeenCalled();
});

it("loads visible reasoning and isolates sessions with the same row id/revision", async () => {
	const reasoning = { ...row, activityKind: "reasoning" as const, detail: { text: "short preview" } };
	get.mockResolvedValue({ data: { ...reasoning, detailOmitted: undefined, detail: { text: "complete reasoning" } } });
	const { rerender } = render(view(reasoning, true));
	expect(await screen.findByText("complete reasoning")).toBeInTheDocument();
	rerender(view(reasoning, true, "s2"));
	await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
	expect(get.mock.calls[1][1].params.path.sessionId).toBe("s2");
});

it("keeps failures retryable and rejects a mismatching revision", async () => {
	get.mockResolvedValueOnce({ data: { ...row, revision: 2, detailOmitted: undefined, detail: { output: "wrong revision" } } });
	render(view(row, true));
	expect(await screen.findByText(/Could not load full activity/)).toBeInTheDocument();
	expect(screen.queryByText("wrong revision")).not.toBeInTheDocument();
	get.mockResolvedValueOnce({ data: { ...row, detailOmitted: undefined, detail: { output: "retried" } } });
	fireEvent.click(screen.getByRole("button", { name: "Retry" }));
	expect(await screen.findByText("retried")).toBeInTheDocument();
});
