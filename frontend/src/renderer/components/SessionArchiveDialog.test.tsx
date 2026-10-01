import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SessionArchiveDialog } from "./SessionArchiveDialog";

describe("SessionArchiveDialog", () => {
	it("warns that ignored files are removed and not restored", () => {
		render(
			<SessionArchiveDialog
				onConfirm={vi.fn()}
				onOpenChange={vi.fn()}
				open
				session={undefined}
				trigger={<button type="button">Archive</button>}
			/>,
		);

		expect(screen.getByText(/Ignored files aren't saved/)).toBeInTheDocument();
	});
});
