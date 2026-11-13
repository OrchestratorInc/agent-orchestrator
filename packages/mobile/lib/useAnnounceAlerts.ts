import { useEffect, useRef } from "react";
import { AccessibilityInfo } from "react-native";

/**
 * Reads each new alert aloud once, for a screen whose alerts are native text
 * with no live-region role of their own. One already shown is not repeated
 * when another appears beside it.
 */
export function useAnnounceAlerts(alerts: readonly string[]) {
	const announced = useRef<ReadonlySet<string>>(new Set());
	const key = alerts.join("\n");
	useEffect(() => {
		const current = key ? key.split("\n") : [];
		for (const alert of current) if (!announced.current.has(alert)) AccessibilityInfo.announceForAccessibility(alert);
		announced.current = new Set(current);
	}, [key]);
}
