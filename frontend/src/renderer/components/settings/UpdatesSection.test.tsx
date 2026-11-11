import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { UpdatesSection } from "./UpdatesSection";
import { useUiStore } from "../../stores/ui-store";
import type { UpdateStatus } from "../../../main/update-settings";
import { appI18n } from "../../i18n";

const {
	updGetStatus,
	updOnStatus,
	updDownload,
	settingsGet,
	settingsSet,
	getVersion,
	featureBuildsList,
	featureBuildsGetActive,
	telemetryCapture,
} = vi.hoisted(() => ({
	updGetStatus: vi.fn(),
	updOnStatus: vi.fn(),
	updDownload: vi.fn(),
	settingsGet: vi.fn(),
	settingsSet: vi.fn(),
	getVersion: vi.fn(),
	featureBuildsList: vi.fn(),
	featureBuildsGetActive: vi.fn(),
	telemetryCapture: vi.fn(),
}));

vi.mock("../../lib/bridge", () => ({
	aoBridge: {
		app: { getVersion },
		updates: {
			getStatus: updGetStatus,
			onStatus: updOnStatus,
			install: vi.fn(),
			download: updDownload,
			check: vi.fn(),
			returnHome: vi.fn(),
		},
		updateSettings: { get: settingsGet, set: settingsSet },
		featureBuilds: { getActive: featureBuildsGetActive, list: featureBuildsList },
	},
}));

vi.mock("../../lib/telemetry", () => ({
	captureRendererEvent: telemetryCapture,
	releaseChannelFrom: vi.fn(),
	setReleaseChannelContext: vi.fn(),
}));

vi.mock("../../hooks/useRequestUpdateInstall", () => ({
	useRequestUpdateInstall: () => () => undefined,
}));

function renderUpdates() {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	render(
		<QueryClientProvider client={queryClient}>
			<UpdatesSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	for (const m of [updGetStatus, updOnStatus, updDownload, settingsGet, settingsSet, getVersion, featureBuildsList, featureBuildsGetActive, telemetryCapture]) {
		m.mockReset();
	}
	updOnStatus.mockReturnValue(() => undefined);
	settingsGet.mockResolvedValue({ enabled: false, channel: "latest", nightlyAck: true, feature: null });
	settingsSet.mockResolvedValue({ enabled: false, channel: "latest", nightlyAck: true, feature: null });
	getVersion.mockResolvedValue("0.12.11-nightly.202609070300");
	featureBuildsList.mockResolvedValue([]);
	featureBuildsGetActive.mockResolvedValue(null);
	updGetStatus.mockResolvedValue({ state: "idle" } satisfies UpdateStatus);
	useUiStore.setState({ developerMode: false });
});

it("renders the installed nightly build time as the device-local instant", async () => {
	// The stamp 202609070300 encodes 03:00 UTC. The expected string is derived
	// from that absolute instant, so the assertion holds in every timezone —
	// and it can only hold at all when the stamp is parsed as UTC, never as
	// local wall time (issue #5059).
	renderUpdates();

	const builtAt = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(
		new Date(Date.UTC(2026, 8, 7, 3, 0)),
	);
	expect(await screen.findByText(`Built ${builtAt}`)).toBeVisible();
});

it("links PRs and commits in downloaded notes from the renamed repository", async () => {
	const repo = "https://github.com/OrchestratorInc/agent-orchestrator";
	const commit = "7b7db96413e6a396a10afbcada0e2fd04a994ad8";
	updGetStatus.mockResolvedValue({
		state: "downloaded",
		version: "0.13.4-nightly.202610051230",
		releaseNotes: [
			"**Changes in this nightly**",
			"",
			`- Refresh startup readiness by @illegalcall in [#6228](${repo}/pull/6228)`,
			"",
			"**Build details**",
			"",
			`- Commit: [7b7db96](${repo}/commit/${commit})`,
		].join("\n"),
	} satisfies UpdateStatus);
	renderUpdates();

	expect(await screen.findByRole("link", { name: "#6228" })).toHaveAttribute("href", `${repo}/pull/6228`);
	expect(screen.getByRole("link", { name: "7b7db96" })).toHaveAttribute("href", `${repo}/commit/${commit}`);
	expect(screen.queryByText(/@illegalcall/)).toBeNull();
});

it("names the newer build when a full disk holds it back (#6607)", async () => {
	updGetStatus.mockResolvedValue({
		state: "error",
		message: "Not enough disk space to install the update. Free up space, then retry.",
		version: "0.13.8-nightly.202610121355",
		staged: { version: "0.13.7-nightly.202610101220", stagedAt: 1, escalated: false, ready: false },
	} satisfies UpdateStatus);
	renderUpdates();

	expect(await screen.findByText(/Not enough disk space to install the update/)).toBeVisible();
	expect(screen.getByText("v0.13.8-nightly.202610121355 is available")).toBeVisible();
});

it("keeps restart available when native staging has stalled", async () => {
	updGetStatus.mockResolvedValue({
		state: "downloaded",
		version: "0.13.7-nightly.202610101220",
		staged: { version: "0.13.7-nightly.202610101220", stagedAt: 1, escalated: false, ready: false },
	} satisfies UpdateStatus);
	renderUpdates();

	expect(await screen.findByText("Updating to v0.13.7-nightly.202610101220")).toBeVisible();
	expect(screen.getByRole("button", { name: "Restart & install" })).toBeVisible();
	expect(screen.getByText("Downloaded. Restart to finish updating.")).toBeVisible();
});

// A dump the main process has not learned to rewrite yet must still stay out
// of Settings: no response body, headers or app.asar paths (#5755).
const UPDATER_DUMP =
	'Cannot parse releases feed: HttpError: 504 Gateway Time-out\nHeaders: {"set-cookie": "_gh_sess=abc123"}\n    at createHttpError (C:\\AO\\resources\\app.asar\\httpExecutor.js:53:12)';

it("shows a short failure instead of an updater dump in the status line (#5755)", async () => {
	updGetStatus.mockResolvedValue({ state: "error", message: UPDATER_DUMP } satisfies UpdateStatus);
	renderUpdates();

	expect(await screen.findByText("Update failed.")).toBeVisible();
	expect(screen.queryByText(/set-cookie|app\.asar|HttpError/)).toBeNull();
});

it("shows a short failure instead of an updater dump beside a staged build (#5755)", async () => {
	updGetStatus.mockResolvedValue({ state: "downloaded", version: "1.2.3", checkError: UPDATER_DUMP } satisfies UpdateStatus);
	renderUpdates();

	expect(await screen.findByText("Update failed.")).toBeVisible();
	expect(screen.queryByText(/set-cookie|app\.asar|HttpError/)).toBeNull();
});

it("shows a rewritten failure in the person's language (#5755)", async () => {
	updGetStatus.mockResolvedValue({
		state: "downloaded",
		version: "1.2.3",
		checkError: "Couldn't check for updates — the update server timed out. Try again in a few minutes.",
		checkErrorInfo: { kind: "timeout", phase: "check" },
	} satisfies UpdateStatus);
	await appI18n.changeLanguage("ja");
	try {
		renderUpdates();

		expect(await screen.findByText(
			"アップデートを確認できませんでした — アップデートサーバーの応答がタイムアウトしました。数分後にもう一度お試しください。",
		)).toBeVisible();
	} finally {
		await appI18n.changeLanguage("en");
	}
});

it("shows a rewritten error status in the person's language (#5755)", async () => {
	updGetStatus.mockResolvedValue({
		state: "error",
		message: "Download failed — the update server isn't available right now. Try again in a few minutes.",
		errorInfo: { kind: "unavailable", phase: "download" },
	} satisfies UpdateStatus);
	await appI18n.changeLanguage("de");
	try {
		renderUpdates();

		expect(await screen.findByText(
			"Download fehlgeschlagen — der Update-Server ist gerade nicht verfügbar. Bitte in einigen Minuten erneut versuchen.",
		)).toBeVisible();
	} finally {
		await appI18n.changeLanguage("en");
	}
});

it.each(["downloaded", "error"] as const)("keeps focus on the live status while downloading until the update reaches %s", async (state) => {
	let emit: (status: UpdateStatus) => void = () => undefined;
	updOnStatus.mockImplementation((listener: typeof emit) => {
		emit = listener;
		return () => undefined;
	});
	updGetStatus.mockResolvedValue({ state: "available", version: "9.9.9" } satisfies UpdateStatus);
	renderUpdates();

	const update = await screen.findByRole("button", { name: "Update to v9.9.9" });
	update.focus();
	fireEvent.click(update);
	// The button leaves once clicked, so it cannot be pressed twice; focus
	// waits on the live status line, which now carries the download.
	const statusLine = screen.getByTestId("update-status-line");
	expect(update).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: /Update to v9\.9\.9/ })).toBeNull();
	expect(statusLine).toHaveFocus();
	expect(updDownload).toHaveBeenCalledTimes(1);
	expect(statusLine).toHaveAttribute("aria-busy", "true");

	act(() => emit({ state: "downloading", version: "9.9.9", percent: 42 }));
	expect(statusLine).toHaveFocus();
	expect(screen.getByRole("progressbar")).toHaveAttribute("value", "42");
	expect(screen.queryByRole("button", { name: "Check for updates" })).toBeNull();
	act(() => emit({ state: "preparing", version: "9.9.9" }));
	expect(statusLine).toHaveFocus();

	act(() => emit({ state, version: "9.9.9", ...(state === "error" ? { message: "Download failed" } : {}) }));
	expect(statusLine).toHaveAttribute("aria-busy", "false");
	if (state === "downloaded") expect(screen.getByRole("button", { name: "Restart & install" })).toHaveFocus();
	else {
		expect(screen.getByText("Download failed")).toBeVisible();
		expect(screen.getByRole("button", { name: "Check for updates" })).toHaveFocus();
	}
	expect(screen.getByRole("button", { name: "Check for updates" })).toBeEnabled();
});

it("names the staged build on Restart when a newer one is only available", async () => {
	// Restart installs the staged 2.0.0; the headline and Update are about 3.0.0.
	updGetStatus.mockResolvedValue({
		state: "available",
		version: "3.0.0",
		staged: { version: "2.0.0", stagedAt: 1, escalated: false, ready: true },
	} satisfies UpdateStatus);
	renderUpdates();

	expect(await screen.findByRole("button", { name: "Update to v3.0.0" })).toBeVisible();
	expect(screen.getByTestId("update-target-version")).toHaveTextContent("v3.0.0");
	expect(screen.getByRole("button", { name: "Restart & install v2.0.0" })).toBeVisible();
});

it("leaves Restart unlabelled when it installs the version in the headline", async () => {
	updGetStatus.mockResolvedValue({
		state: "downloaded",
		version: "2.0.0",
		staged: { version: "2.0.0", stagedAt: 1, escalated: false, ready: true },
	} satisfies UpdateStatus);
	renderUpdates();

	expect(await screen.findByRole("button", { name: "Restart & install" })).toBeVisible();
	expect(screen.queryByTestId("update-staged-version")).toBeNull();
});

it("hands focus on when Check for updates is removed under it", async () => {
	let emit: (status: UpdateStatus) => void = () => undefined;
	updOnStatus.mockImplementation((listener: typeof emit) => {
		emit = listener;
		return () => undefined;
	});
	updGetStatus.mockResolvedValue({ state: "not-available" } satisfies UpdateStatus);
	renderUpdates();

	const check = await screen.findByRole("button", { name: "Check for updates" });
	check.focus();
	// A background check finds an update: Check gives way to Update.
	act(() => emit({ state: "available", version: "9.9.9" }));
	expect(check).not.toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Update to v9.9.9" })).toHaveFocus();
});

it("does not take focus from elsewhere when Check for updates is removed", async () => {
	let emit: (status: UpdateStatus) => void = () => undefined;
	updOnStatus.mockImplementation((listener: typeof emit) => {
		emit = listener;
		return () => undefined;
	});
	updGetStatus.mockResolvedValue({ state: "not-available" } satisfies UpdateStatus);
	renderUpdates();

	await screen.findByRole("button", { name: "Check for updates" });
	const elsewhere = screen.getByRole("switch", { name: "Automatic updates" });
	elsewhere.focus();
	act(() => emit({ state: "downloading", version: "9.9.9", percent: 10 }));
	expect(elsewhere).toHaveFocus();
});

it("formats the downloaded size with the active locale's decimal separator", async () => {
	updGetStatus.mockResolvedValue({ state: "downloading", version: "9.9.9", percent: 57, transferred: 142_700_000, total: 250_400_000 } satisfies UpdateStatus);
	await appI18n.changeLanguage("de");
	try {
		renderUpdates();
		expect(await screen.findByText(/142,7/)).toBeInTheDocument();
		expect(screen.getByText(/250,4/)).toBeInTheDocument();
	} finally {
		await appI18n.changeLanguage("en");
	}
});

it.each(["downloading", "preparing"] as const)("shows an existing %s update without a click", async (state) => {
	updGetStatus.mockResolvedValue({ state, version: "9.9.9" } satisfies UpdateStatus);
	renderUpdates();

	// The status line alone carries a download already under way: no button
	// to press, and nothing takes focus.
	const statusLine = await screen.findByTestId("update-status-line");
	await waitFor(() => expect(statusLine).toHaveAttribute("aria-busy", "true"));
	expect(screen.queryByRole("button", { name: /Update to v9\.9\.9/ })).toBeNull();
	expect(screen.queryByRole("button", { name: "Check for updates" })).toBeNull();
	expect(statusLine).not.toHaveFocus();
	expect(updDownload).not.toHaveBeenCalled();
});

it.each(["downloaded", "error"] as const)("does not steal focus when an update reaches %s", async (state) => {
	let emit: (status: UpdateStatus) => void = () => undefined;
	updOnStatus.mockImplementation((listener: typeof emit) => {
		emit = listener;
		return () => undefined;
	});
	updGetStatus.mockResolvedValue({ state: "available", version: "9.9.9" } satisfies UpdateStatus);
	renderUpdates();
	const update = await screen.findByRole("button", { name: "Update to v9.9.9" });
	update.focus();
	fireEvent.click(update);
	const automaticUpdates = screen.getByRole("switch");
	automaticUpdates.focus();

	act(() => emit({ state, version: "9.9.9" }));
	expect(automaticUpdates).toHaveFocus();
});

it("focuses the status after a fast failure while the manual check action is still busy", async () => {
	let emit: (status: UpdateStatus) => void = () => undefined;
	updOnStatus.mockImplementation((listener: typeof emit) => {
		emit = listener;
		return () => undefined;
	});
	renderUpdates();
	fireEvent.click(await screen.findByRole("button", { name: "Check for updates" }));
	act(() => emit({ state: "available", version: "9.9.9" }));
	const update = screen.getByRole("button", { name: "Update to v9.9.9" });
	update.focus();
	fireEvent.click(update);

	act(() => emit({ state: "error", message: "Download failed" }));
	expect(screen.getByRole("button", { name: "Checking for updates…" })).toBeDisabled();
	expect(screen.getByTestId("update-status-line")).toHaveFocus();
});
