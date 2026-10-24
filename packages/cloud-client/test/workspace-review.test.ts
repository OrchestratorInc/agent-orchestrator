import { describe, expect, it, vi } from "vitest";

import {
  createCloudClient,
  type WorkspaceRepository,
  type WorkspaceReviewFileResponse,
  type WorkspaceReviewFileSummary,
  type WorkspaceReviewResponse,
  type WorkspaceReviewSearchResponse,
  type WorkspaceReviewTreeResponse,
} from "../src/index.js";

const file: WorkspaceReviewFileSummary = {
  path: "src/new file.ts",
  status: "untracked",
  additions: 1,
  deletions: 0,
  size: 11,
  binary: false,
  editable: true,
  fileFingerprint: "file-v1",
};

const repository: WorkspaceRepository = {
  name: "api",
  label: "API",
  repository: "acme/api",
  branch: "main",
  present: true,
};

const review: WorkspaceReviewResponse = {
  workspaceVersion: "workspace-v1",
  compareBaseSha: "base-sha",
  compareBaseRef: "origin/main",
  compareMode: "base",
  files: [file],
  sections: { committed: [], staged: [], unstaged: [], untracked: [file] },
  commits: [],
  summary: { files: 1, additions: 1, deletions: 0 },
  ahead: 2,
  behind: 0,
  truncated: false,
  repositories: [{ name: "repository", label: "app", main: true, present: true }, repository],
};

const detail: WorkspaceReviewFileResponse = {
  ...file,
  deleted: false,
  content: "export {};\n",
  contentTruncated: false,
  diff: "@@ -0,0 +1 @@\n+export {};\n",
  diffTruncated: false,
  compareBaseSha: "base-sha",
  compareBaseRef: "origin/main",
  compareMode: "base",
  workspaceVersion: "workspace-v1",
};

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("Cloud workspace review", () => {
  it("reads the selected repository's review inventory and sections", async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(review));
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "token", fetch: fetchMock });
    const controller = new AbortController();

    await expect(client.getWorkspaceReview("org/1", "session/1", { repository: "acme/api", signal: controller.signal })).resolves.toEqual(review);

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/review?repository=acme%2Fapi");
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: "GET", signal: controller.signal });
    expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get("Authorization")).toBe("Bearer token");
  });

  it("reads one scoped file with both its content and unified diff", async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(detail));
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "token", fetch: fetchMock });
    const controller = new AbortController();

    await expect(client.getWorkspaceReviewFile("org/1", "session/1", { path: "src/new file.ts", scope: "untracked", repository: "api" }, { signal: controller.signal })).resolves.toEqual(detail);

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/review/file?path=src%2Fnew+file.ts&scope=untracked&repository=api");
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: "GET", signal: controller.signal });
  });

  it("reads a directory from the selected repository's complete file tree", async () => {
    const tree: WorkspaceReviewTreeResponse = { path: "src/lib", entries: [{ name: "new file.ts", path: "src/lib/new file.ts", type: "file", status: "copied", size: 11, binary: false }, { name: "nested", path: "src/lib/nested", type: "dir", hasChanges: true }], truncated: false };
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(tree));
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "token", fetch: fetchMock });
    const controller = new AbortController();

    await expect(client.getWorkspaceReviewTree("org/1", "session/1", { path: "src/lib", repository: "api" }, { signal: controller.signal })).resolves.toEqual(tree);

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/tree?path=src%2Flib&repository=api");
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: "GET", signal: controller.signal });
  });

  it("searches repository files with the opaque cursor and bounded page size", async () => {
    const page: WorkspaceReviewSearchResponse = { query: "export +", results: [{ path: file.path, status: file.status, size: file.size, binary: file.binary, fileFingerprint: file.fileFingerprint }], nextCursor: "next/cursor+", truncated: true };
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(page));
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "token", fetch: fetchMock });
    const controller = new AbortController();

    await expect(client.searchWorkspaceReview("org/1", "session/1", { query: "export +", limit: 25, cursor: "older/cursor+", repository: "api" }, { signal: controller.signal })).resolves.toEqual(page);

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/search?query=export+%2B&cursor=older%2Fcursor%2B&limit=25&repository=api");
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: "GET", signal: controller.signal });
  });

  const operations = [
    ["summary", (client: ReturnType<typeof createCloudClient>, signal?: AbortSignal) => client.getWorkspaceReview("org", "session", { signal }), "/workspace/review"],
    ["file", (client: ReturnType<typeof createCloudClient>, signal?: AbortSignal) => client.getWorkspaceReviewFile("org", "session", { path: "README.md" }, { signal }), "/workspace/review/file?path=README.md"],
    ["tree", (client: ReturnType<typeof createCloudClient>, signal?: AbortSignal) => client.getWorkspaceReviewTree("org", "session", undefined, { signal }), "/workspace/tree"],
    ["search", (client: ReturnType<typeof createCloudClient>, signal?: AbortSignal) => client.searchWorkspaceReview("org", "session", { query: "README" }, { signal }), "/workspace/search?query=README"],
  ] as const;

  it.each(operations)("leaves omitted %s query parameters to the server defaults", async (_name, request, suffix) => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse({}));
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "token", fetch: fetchMock });

    await request(client);

    expect(fetchMock.mock.calls[0]?.[0]).toBe(`https://cloud.test/api/cloud/v1/orgs/org/sessions/session${suffix}`);
    expect(fetchMock.mock.calls[0]?.[1]?.body).toBeUndefined();
  });

  it.each(operations)("preserves worker error envelopes for %s", async (_name, request) => {
    const envelope = { error: "Conflict", code: "WORKER_UNAVAILABLE", message: "The session worker is not connected.", requestId: "workspace-request-1" };
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "token", fetch: async () => jsonResponse(envelope, 409) });

    await expect(request(client)).rejects.toMatchObject({ name: "CloudApiError", status: 409, envelope, requestId: "workspace-request-1" });
  });

  it.each(operations)("cancels %s before acquiring account credentials", async (_name, request) => {
    const controller = new AbortController();
    const cancelled = new Error("Cloud account changed.");
    controller.abort(cancelled);
    const getAccessToken = vi.fn(() => "token");
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse({}));
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken, fetch: fetchMock });

    await expect(request(client, controller.signal)).rejects.toBe(cancelled);
    expect(getAccessToken).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
