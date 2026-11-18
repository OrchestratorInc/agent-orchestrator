import { describe, expect, it } from "vitest";
import { parseSharedArtifactLink, sharedArtifactAppURL, sharedArtifactURL } from "../../../packages/shared/artifacts/shared-artifact-link";

const viewer = "https://orchestrator.inc/artifact/abcdefghijklmnopqrstuvwxyz";
const link = `ao-app://shared-artifact?url=${encodeURIComponent(viewer)}`;

describe("published artifact links", () => {
	it("opens the exact published viewer, without any session lookup", () => {
		expect(parseSharedArtifactLink(link)).toBe(viewer);
		expect(sharedArtifactAppURL(viewer)).toBe(`${viewer}?inApp=1`);
		expect(sharedArtifactURL("https://orchestrator.inc/artifact/my-renamed-report")).toBe("https://orchestrator.inc/artifact/my-renamed-report");
	});
	it("refuses credentials, tokens, fragments, non-HTTPS and non-artifact destinations", () => {
		for (const value of ["not a URL", viewer.replace("https:", "http:"), viewer.replace("https:", "file:"),
			viewer.replace("https://", "https://user:secret@"), `${viewer}?token=secret`, `${viewer}#fragment`,
			viewer.replace("/artifact/", "/session/"), `${viewer}/extra`, viewer.replace("abcdefghijklmnopqrstuvwxyz", "../private")]) {
			expect(sharedArtifactURL(value)).toBeNull();
			expect(parseSharedArtifactLink(`ao-app://shared-artifact?url=${encodeURIComponent(value)}`)).toBeNull();
		}
	});
	it("refuses ambiguous or unrelated deep links", () => {
		for (const value of [link.replace("ao-app:", "https:"), link.replace("shared-artifact?", "shared-artifact/extra?"),
			`${link}&url=${encodeURIComponent(viewer)}`, `${link}&other=x`, `${link}#fragment`, "ao-app://shared-artifact", "ao-app://callback"]) {
			expect(parseSharedArtifactLink(value)).toBeNull();
		}
	});
});
