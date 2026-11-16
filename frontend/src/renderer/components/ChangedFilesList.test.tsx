import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CHANGED_FILES_SHOWN, ChangedFilesList } from "./ChangedFilesList";

const file = (path: string, status: "added" | "modified" | "deleted" | "renamed" = "modified", additions = 1, deletions = 0) => ({ path, status, additions, deletions, size: 1, binary: false });

describe("ChangedFilesList", () => {
	it("lists files flat in path order with the folder beside the name, and no folder rows", async () => {
		const onOpenFile = vi.fn();
		render(<ChangedFilesList files={[file("src/b.ts", "modified", 2, 1), file("README.md", "added", 4, 0)]} id="l" onOpenAll={() => {}} onOpenFile={onOpenFile} />);
		const rows = screen.getAllByRole("listitem");
		expect(rows.map((row) => row.textContent)).toEqual(["README.md+4A, added", "b.tssrc+2−1M, modified", "View all changes"]);
		await userEvent.click(screen.getByRole("button", { name: /b\.ts/ }));
		expect(onOpenFile).toHaveBeenCalledWith("src/b.ts");
	});

	it("leaves out a count with nothing on it", () => {
		render(<ChangedFilesList files={[file("a.ts", "modified", 0, 3), file("b.png", "added", 0, 0)]} id="l" onOpenAll={() => {}} />);
		expect(screen.getByRole("button", { name: /a\.ts/ })).toHaveTextContent(/^a\.ts−3M/);
		expect(screen.getByRole("button", { name: /b\.png/ })).not.toHaveTextContent(/[+−]/);
	});

	it("shows the first few and leaves the rest to the full view", async () => {
		const files = Array.from({ length: CHANGED_FILES_SHOWN + 3 }, (_, index) => file(`f${String(index).padStart(2, "0")}.ts`, "deleted"));
		const onOpenAll = vi.fn();
		render(<ChangedFilesList files={files} id="l" onOpenAll={onOpenAll} />);
		expect(screen.getAllByRole("button", { name: /\.ts.*, deleted/ })).toHaveLength(CHANGED_FILES_SHOWN);
		await userEvent.click(screen.getByRole("button", { name: "View all changes (+3 more files)" }));
		expect(onOpenAll).toHaveBeenCalled();
	});
});
