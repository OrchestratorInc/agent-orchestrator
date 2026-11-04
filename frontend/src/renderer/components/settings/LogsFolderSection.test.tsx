import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { aoBridge } from "../../lib/bridge";
import { LogsFolderSection } from "./LogsFolderSection";

vi.mock("../../lib/bridge", () => ({
	aoBridge: {
		app: {
			getLogsDirectory: vi.fn(),
			openLogsFolder: vi.fn(),
		},
	},
}));

const getLogsDirectory = aoBridge.app.getLogsDirectory as ReturnType<typeof vi.fn>;
const openLogsFolder = aoBridge.app.openLogsFolder as ReturnType<typeof vi.fn>;

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	getLogsDirectory.mockReset();
	openLogsFolder.mockReset();
	getLogsDirectory.mockResolvedValue("~/.ao/logs");
	openLogsFolder.mockResolvedValue(undefined);
});

describe("LogsFolderSection", () => {
	it("shows where the logs live and opens the folder", async () => {
		const user = userEvent.setup();
		render(<LogsFolderSection />);

		expect(await screen.findByText(/Recent logs stay on this computer in ~\/\.ao\/logs\./)).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Open logs folder" }));
		expect(openLogsFolder).toHaveBeenCalledTimes(1);
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("reports a folder that cannot be opened", async () => {
		const user = userEvent.setup();
		openLogsFolder.mockRejectedValue(new Error("denied"));
		render(<LogsFolderSection />);

		await user.click(screen.getByRole("button", { name: "Open logs folder" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not open the logs folder.");
	});
});
