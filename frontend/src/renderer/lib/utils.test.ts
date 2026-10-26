import { describe, expect, it } from "vitest";
import { cn } from "./utils";

describe("cn", () => {
	it("keeps a custom font size next to a text colour", () => {
		expect(cn("text-caption", "text-muted-foreground")).toBe("text-caption text-muted-foreground");
		expect(cn("text-control text-foreground")).toBe("text-control text-foreground");
	});

	it("lets a later custom font size replace an earlier one", () => {
		expect(cn("text-xs", "text-caption")).toBe("text-caption");
		expect(cn("text-caption", "text-subtitle")).toBe("text-subtitle");
	});
});
