import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { CloudTerminalAttachments } from "./CloudTerminalAttachments";
const h = vi.hoisted(() => ({ upload: vi.fn(), materialize: vi.fn() }));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { materializeAttachments: h.materialize },
		baseUrl: "https://cloud.test",
		userId: "user",
	}),
}));
vi.mock("../lib/cloud-attachments", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/cloud-attachments")>()),
	uploadCloudAttachments: h.upload,
}));
const session = {
	id: "session",
	workspaceId: "project",
	cloud: { orgId: "org" },
	kind: "worker",
} as WorkspaceSession;
beforeEach(() => {
	h.upload
		.mockReset()
		.mockImplementation(async (_c, _url, _org, _project, _session, files) =>
			files.map((a: { id: string }) => ({ ...a, file: undefined, attachmentId: "image-id" })),
		);
	h.materialize.mockReset().mockResolvedValue({ paths: [".ao/attachments/image-id.png"], epoch: 4 });
});
it("waits for acknowledged sandbox paths before insertion and does not submit", async () => {
	let resolve!: (value: unknown) => void;
	h.materialize.mockImplementationOnce(
		() =>
			new Promise((r) => {
				resolve = r;
			}),
	);
	const insert = vi.fn(() => true);
	const { container } = render(
		<CloudTerminalAttachments session={session} disabled={false} onInsert={insert} />,
	);
	fireEvent.change(container.querySelector('input[type="file"]')!, {
		target: { files: [new File(["image"], "image.png", { type: "image/png" })] },
	});
	await waitFor(() => expect(h.materialize).toHaveBeenCalledWith("org", "session", ["image-id"]));
	expect(insert).not.toHaveBeenCalled();
	await act(async () => resolve({ paths: [".ao/attachments/image-id.png"], epoch: 4 }));
	await waitFor(() => expect(insert).toHaveBeenCalledWith([".ao/attachments/image-id.png"], 4));
	expect(screen.getByText("Inserts a path. Press Enter yourself to submit.")).toBeInTheDocument();
});
it("rejects an acknowledgement after changing session and keeps retry controls", async () => {
	let resolve!: (value: unknown) => void;
	h.materialize.mockImplementationOnce(
		() =>
			new Promise((r) => {
				resolve = r;
			}),
	);
	const insert = vi.fn(() => true);
	const { container, rerender } = render(
		<CloudTerminalAttachments session={session} disabled={false} onInsert={insert} />,
	);
	fireEvent.change(container.querySelector('input[type="file"]')!, {
		target: { files: [new File(["image"], "image.png", { type: "image/png" })] },
	});
	await waitFor(() => expect(h.materialize).toHaveBeenCalled());
	rerender(
		<CloudTerminalAttachments
			session={{ ...session, id: "replacement" }}
			disabled={false}
			onInsert={insert}
		/>,
	);
	await act(async () => resolve({ paths: [".ao/attachments/image-id.png"], epoch: 4 }));
	expect(insert).not.toHaveBeenCalled();
	expect(await screen.findByRole("alert")).toHaveTextContent("terminal changed");
});
