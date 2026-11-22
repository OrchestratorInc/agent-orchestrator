import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DevicePanel } from "./DevicePanel";
import { TooltipProvider } from "./ui/tooltip";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

const playMjpeg = vi.fn(() => ({ stop: vi.fn() }));
const playH264 = vi.fn(() => ({ stop: vi.fn() }));
vi.mock("../lib/device-stream", () => ({ playMjpeg: (...args: unknown[]) => playMjpeg(...(args as [])), playH264: (...args: unknown[]) => playH264(...(args as [])) }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	apiErrorMessage: (error: unknown) => (error && typeof error === "object" && "message" in error ? String(error.message) : ""),
	getApiBaseUrl: () => "http://127.0.0.1:3001",
}));

class FakeSocket {
	static instances: FakeSocket[] = [];
	static OPEN = 1;
	readyState = 0;
	binaryType = "blob";
	sent: unknown[] = [];
	onopen: (() => void) | null = null;
	onclose: (() => void) | null = null;
	onerror: (() => void) | null = null;
	constructor(readonly url: string) {
		FakeSocket.instances.push(this);
	}
	send(data: unknown) {
		this.sent.push(data);
	}
	close() {
		this.readyState = 3;
	}
	open() {
		this.readyState = FakeSocket.OPEN;
		this.onopen?.();
	}
}

const iosLease = {
	sessionId: "ao-1",
	provider: "local",
	reason: "Runs on this machine",
	device: { id: "UDID-1", name: "iPhone 17 Pro", platform: "ios", version: "iOS 26.5", booted: true },
	agentDevice: { command: "/ao/agent-device", args: [] },
	openedAt: "2026-10-26T00:00:00Z",
};

function status(overrides: Record<string, unknown> = {}) {
	return {
		enabled: true,
		state: "ready",
		platforms: [{ platform: "ios", available: true }, { platform: "android", available: false, reason: "Install the Android SDK." }],
		devices: [
			{ id: "UDID-1", name: "iPhone 17 Pro", platform: "ios", version: "iOS 26.5", booted: true },
			{ id: "UDID-2", name: "iPhone Air", platform: "ios", version: "iOS 26.5", booted: false },
		],
		leases: [{ ...iosLease, sessionId: "ao-2", device: { id: "UDID-2", name: "iPhone Air", platform: "ios", booted: false } }],
		...overrides,
	};
}

function mockApi({ deviceStatus = status(), lease = null as unknown }: { deviceStatus?: unknown; lease?: unknown } = {}) {
	getMock.mockImplementation(async (path: string) => {
		if (path === "/api/v1/devices") return { data: deviceStatus };
		return { data: { lease } };
	});
	postMock.mockResolvedValue({ data: {} });
}

function renderPanel(ui: ReactNode) {
	return render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<TooltipProvider>{ui}</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("DevicePanel", () => {
	beforeEach(() => {
		FakeSocket.instances = [];
		vi.stubGlobal("WebSocket", FakeSocket);
	});
	afterEach(() => {
		vi.unstubAllGlobals();
		getMock.mockReset();
		postMock.mockReset();
	});

	it("offers to turn device support on, with what this machine is missing", async () => {
		mockApi({ deviceStatus: status({ enabled: false, state: "off", devices: [], leases: [] }) });
		renderPanel(<DevicePanel isActive sessionId="ao-1" />);
		await userEvent.click(await screen.findByRole("button", { name: "Turn on device support" }));
		expect(postMock).toHaveBeenCalledWith("/api/v1/devices/setup");
		expect(screen.getByText(/Install the Android SDK\./)).toBeInTheDocument();
	});

	it("offers to turn support back on rather than show a device kept from before it was off", async () => {
		mockApi({ deviceStatus: status({ enabled: false, state: "off", devices: [], leases: [iosLease] }), lease: iosLease });
		renderPanel(<DevicePanel isActive sessionId="ao-1" />);
		expect(await screen.findByRole("button", { name: "Turn on device support" })).toBeInTheDocument();
		expect(FakeSocket.instances).toHaveLength(0);
	});

	it("says what to install instead of offering to turn on when nothing can run", async () => {
		mockApi({
			deviceStatus: status({
				enabled: false,
				state: "off",
				devices: [],
				leases: [],
				platforms: [
					{ platform: "ios", available: false, reason: "Install Xcode." },
					{ platform: "android", available: false, reason: "Install the Android SDK." },
				],
			}),
		});
		renderPanel(<DevicePanel isActive sessionId="ao-1" />);
		expect(await screen.findByText("This machine can't run one yet.")).toBeInTheDocument();
		expect(screen.getByText(/Install Xcode\./)).toBeInTheDocument();
		expect(screen.getByText(/Install the Android SDK\./)).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Turn on device support" })).not.toBeInTheDocument();
	});

	it("opens a free device and blocks one another session holds", async () => {
		mockApi();
		renderPanel(<DevicePanel isActive sessionId="ao-1" />);
		expect(await screen.findByText("iPhone 17 Pro")).toBeInTheDocument();
		const buttons = screen.getAllByRole("button", { name: "Open" });
		expect(buttons[1]).toBeDisabled();
		expect(screen.getByText(/In use by ao-2/)).toBeInTheDocument();
		await userEvent.click(buttons[0]);
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/device/open", {
			params: { path: { sessionId: "ao-1" } },
			body: { deviceId: "UDID-1", platform: "ios" },
		});
	});

	it("streams the leased device and sends taps and buttons over the input socket", async () => {
		mockApi({ lease: iosLease });
		renderPanel(<DevicePanel isActive sessionId="ao-1" />);
		// iOS draws the MJPEG stream on a canvas, without the daemon's repeated frame.
		await waitFor(() => expect(playMjpeg).toHaveBeenCalled());
		expect((playMjpeg.mock.calls[0] as unknown[])[0]).toBe("http://127.0.0.1:3001/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/UDID-1/stream.mjpeg?repeat=0");
		expect(playH264).not.toHaveBeenCalled();
		expect(screen.getByText("iOS 26.5")).toBeInTheDocument();
		await waitFor(() => expect(FakeSocket.instances).toHaveLength(1));
		const socket = FakeSocket.instances[0];
		expect(socket.url).toBe("ws://127.0.0.1:3001/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/ws?device=UDID-1");
		socket.open();
		// iOS Home goes through the daemon's device driver: the simulator's own
		// Home button does nothing on a Face ID iPhone.
		await userEvent.click(await screen.findByRole("button", { name: "Home" }));
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/device/button", { params: { path: { sessionId: "ao-1" } }, body: { button: "home" } });
		expect(socket.sent).toHaveLength(0);
		expect(screen.queryByRole("button", { name: "Back" })).not.toBeInTheDocument();

		// Releasing and shutting down are named in the device menu, as on the phone.
		await userEvent.click(screen.getByRole("button", { name: "Device options" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Release device" }));
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/device/close", { params: { path: { sessionId: "ao-1" } }, body: {} });
		await userEvent.click(screen.getByRole("button", { name: "Device options" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Shut down device" }));
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/device/close", { params: { path: { sessionId: "ao-1" } }, body: { shutdown: true } });
	});

	it("says a leased device shut down outside AO is off and starts it again", async () => {
		mockApi({ lease: { ...iosLease, device: { ...iosLease.device, booted: false } } });
		renderPanel(<DevicePanel isActive sessionId="ao-1" />);
		expect(await screen.findByText("iPhone 17 Pro is off")).toBeInTheDocument();
		expect(screen.getByRole("img", { name: "iPhone 17 Pro screen", hidden: true })).toHaveClass("hidden");
		await userEvent.click(screen.getByRole("button", { name: "Start" }));
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/device/open", {
			params: { path: { sessionId: "ao-1" } },
			body: { deviceId: "UDID-1", platform: "ios" },
		});
	});

	it("loads nothing while the tab is hidden", () => {
		mockApi({ lease: iosLease });
		renderPanel(<DevicePanel isActive={false} sessionId="ao-1" />);
		expect(getMock).not.toHaveBeenCalled();
		expect(FakeSocket.instances).toHaveLength(0);
	});
});
