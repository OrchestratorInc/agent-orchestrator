import { describe, expect, it, vi } from "vitest";

vi.mock("../cloud/useCanCreateCloudProject", () => ({}));

import { placementFor } from "./placement";

const base = { chosen: null, cloudOffer: "yes", localOffered: true, busy: false, shown: null } as const;

describe("placementFor", () => {
	it("opens Local first, and Cloud with no machine", () => {
		expect(placementFor(base)).toBe("local");
		expect(placementFor({ ...base, localOffered: false })).toBe("cloud");
	});

	it("keeps an explicit Cloud request while the plan loads, and drops it once Cloud is refused", () => {
		expect(placementFor({ ...base, chosen: "cloud", cloudOffer: "pending" })).toBe("cloud");
		expect(placementFor({ ...base, chosen: "cloud", cloudOffer: "no" })).toBe("local");
	});

	it("holds the face shown while a create runs, even when its machine disconnects", () => {
		expect(placementFor({ ...base, localOffered: false, busy: true, shown: "local" })).toBe("local");
		expect(placementFor({ ...base, chosen: "local", cloudOffer: "no", busy: true, shown: "cloud" })).toBe("cloud");
		// Once it ends, availability decides again.
		expect(placementFor({ ...base, localOffered: false, busy: false, shown: "local" })).toBe("cloud");
	});
});
