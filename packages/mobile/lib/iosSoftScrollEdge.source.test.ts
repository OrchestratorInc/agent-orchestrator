import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";

const require = createRequire(import.meta.url);
const { adoptSoftScrollEdge } = require("../plugins/withIosSoftScrollEdge") as { adoptSoftScrollEdge: (contents: string) => string };

const TEMPLATE = `@main
class AppDelegate: ExpoAppDelegate {
  public override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
  ) -> Bool {
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }
}
`;

// iOS 27 resolves a scroll view's automatic top edge to the hard style; the
// glass bars want iOS 26's soft blur.
describe("iOS soft scroll edge", () => {
	it("gives every scroll view the soft top edge from launch, even one set back to automatic", () => {
		const out = adoptSoftScrollEdge(TEMPLATE);
		expect(out).toContain("    UIScrollView.adoptSoftTopEdge()\n\n    return super.application(");
		expect(out).toContain("ao_setSoftStyle(style == .automatic ? .soft : style)");
	});

	// iOS 26 already resolves automatic to soft; only iOS 27 needs UIKit patched.
	it("swaps UIKit's methods on iOS 27 only", () => {
		const out = adoptSoftScrollEdge(TEMPLATE);
		expect(out).toContain("guard #available(iOS 27, *) else { return }\n    ao_swizzle(");
		expect(out).not.toContain("guard #available(iOS 26, *)");
	});

	it("applies once", () => {
		const once = adoptSoftScrollEdge(TEMPLATE);
		expect(adoptSoftScrollEdge(once)).toBe(once);
	});

	it("refuses an AppDelegate it no longer recognises", () => {
		expect(() => adoptSoftScrollEdge("class AppDelegate {}")).toThrow(/no longer matches/);
	});
});
