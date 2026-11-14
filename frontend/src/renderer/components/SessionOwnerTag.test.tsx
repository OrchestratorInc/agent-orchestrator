import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SessionOwnerTag } from "./SessionOwnerTag";

describe("SessionOwnerTag", () => {
	it("names the guest a session was started for, and nothing otherwise", () => {
		const tagged = render(<SessionOwnerTag session={{ cloud: { orgId: "org", requestedBy: { userId: "u1", name: "Vaanya" } } }} />);
		expect(tagged.container.textContent).toBe("Vaanya's session");
		tagged.unmount();
		const plain = render(<SessionOwnerTag session={{ cloud: { orgId: "org" } }} />);
		expect(plain.container.textContent).toBe("");
	});
});
