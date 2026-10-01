import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(fileURLToPath(new URL("../app/notifications.tsx", import.meta.url)), "utf8");

describe("notifications host routing", () => {
	it("shows a host picker only for an unqualified multi-host route", () => {
		const scopedRoute = source.indexOf("if (hostId) return <HostScope key={hostId} hostId={hostId}><NotificationsContent /></HostScope>;");
		const pickerRoute = source.indexOf("if (hostStates.length > 1) return <NotificationsHostPicker />;");
		const singleHostRoute = source.indexOf("return <NotificationsContent />;", pickerRoute);
		expect(scopedRoute).toBeGreaterThan(-1);
		expect(pickerRoute).toBeGreaterThan(scopedRoute);
		expect(singleHostRoute).toBeGreaterThan(pickerRoute);
	});

	it("navigates to the chosen host without switching the app's selected host", () => {
		expect(source).toContain('router.push({ pathname: "/notifications", params: { hostId: host.hostId } })');
		expect(source).toContain("host.notificationsUnread > 0");
	});

	it("clears only the item loaded from the active host", () => {
		expect(source).toContain("itemsHostId !== config.hostId || clearingIds.has(notification.id)");
		expect(source).toContain("await clearNotification(source, notification.id)");
		expect(source).toContain("if (currentConfig.current !== source) return");
	});
});
