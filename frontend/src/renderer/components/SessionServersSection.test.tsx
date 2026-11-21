import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import type { WorkspaceSession } from "../types/workspace";
import { SessionServersSection } from "./SessionServersSection";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../lib/host-clients", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/host-clients")>()),
	clientForSessionHost: () => ({ GET: getMock, POST: postMock }),
}));

const session = { id: "ao-1", workspaceId: "p", hostId: "box-a", title: "Fix login", status: "working", mode: "chat" } as unknown as WorkspaceSession;

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<SessionServersSection hostId="box-a" session={session} visible />
		</QueryClientProvider>,
	);
}

describe("SessionServersSection", () => {
	afterEach(() => vi.restoreAllMocks());

	it("opens a server in the Browser and forwards it from a remote host", async () => {
		getMock.mockResolvedValue({ data: { servers: [{ port: 5173, pid: 41, command: "node" }] } });
		postMock.mockResolvedValue({ data: {} });
		vi.spyOn(aoBridge.remotes, "loopbackReach").mockResolvedValue("tunnel");
		const forwards: Array<{ hostId: string; remotePort: number; localPort: number }> = [];
		vi.spyOn(aoBridge.remotes, "portForwards").mockImplementation(async () => [...forwards]);
		const forwardPort = vi.spyOn(aoBridge.remotes, "forwardPort").mockImplementation(async (hostId, port) => {
			const forward = { hostId, remotePort: port, localPort: 51000 };
			forwards.push(forward);
			return forward;
		});
		renderSection();

		fireEvent.click(await screen.findByRole("button", { name: "Open port 5173 in Browser" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/preview", expect.objectContaining({ body: { url: "http://localhost:5173/" } })));

		fireEvent.click(await screen.findByRole("button", { name: "Forward port 5173 to this computer" }));
		expect(forwardPort).toHaveBeenCalledWith("box-a", 5173);
		expect(await screen.findByText("localhost:51000")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Stop forwarding port 5173" })).toBeInTheDocument();
	});

	it("keeps a forward stoppable after its server stops listening", async () => {
		getMock.mockResolvedValue({ data: { servers: [] } });
		vi.spyOn(aoBridge.remotes, "loopbackReach").mockResolvedValue("tunnel");
		vi.spyOn(aoBridge.remotes, "portForwards").mockResolvedValue([{ hostId: "box-a", remotePort: 3000, localPort: 3000 }, { hostId: "box-b", remotePort: 4000, localPort: 4000 }]);
		const stop = vi.spyOn(aoBridge.remotes, "stopPortForward").mockResolvedValue(undefined);
		renderSection();

		fireEvent.click(await screen.findByRole("button", { name: "Stop forwarding port 3000" }));
		expect(stop).toHaveBeenCalledWith("box-a", 3000);
		expect(screen.queryByText("4000")).toBeNull();
	});

	it("drops the servers once the session stops", async () => {
		getMock.mockResolvedValue({ data: { servers: [{ port: 5173, pid: 41, command: "node" }] } });
		vi.spyOn(aoBridge.remotes, "loopbackReach").mockResolvedValue("tunnel");
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = (current: WorkspaceSession) => (
			<QueryClientProvider client={client}>
				<SessionServersSection hostId="box-a" session={current} visible />
			</QueryClientProvider>
		);
		const { rerender } = render(view(session));
		expect(await screen.findByText("node")).toBeInTheDocument();
		rerender(view({ ...session, status: "terminated", isTerminated: true } as WorkspaceSession));
		expect(screen.queryByTestId("session-servers")).toBeNull();
	});

	it("offers no forward for a host on this computer", async () => {
		getMock.mockResolvedValue({ data: { servers: [{ port: 5173, pid: 41, command: "node" }] } });
		const reach = vi.spyOn(aoBridge.remotes, "loopbackReach").mockResolvedValue("same");
		renderSection();
		expect(await screen.findByText("node")).toBeInTheDocument();
		await waitFor(() => expect(reach).toHaveBeenCalled());
		expect(screen.queryByRole("button", { name: /Forward port/ })).toBeNull();
	});
});
