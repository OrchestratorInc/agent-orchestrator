import { useMemo } from "react";
import { Platform } from "react-native";
import { classifyConnectionFailure, describeConnectionFailure, type ConnectionErrorCopy } from "./connectionError";
import { tunnelMayHaveRotated } from "./staleTunnel";
import { useApp } from "./store";

/**
 * The board poll's failure as human copy, shared by every tab so the same
 * failure reads the same on Workers, Projects and PRs.
 *
 * A failed poll is only described as the machine's failure once the link is
 * classified as unreachable; see classifyLink.
 */
export function useBoardFailure(): ConnectionErrorCopy {
	const { errorStatus, connection, config, activeEndpoints, linkHealth, selectedHostName } = useApp();
	return useMemo(() => {
		// Neither of these is the machine's fault, so neither may say it is offline.
		if (linkHealth === "phone-offline") {
			return {
				title: "You're offline",
				icon: "wifi-off",
				hint: "Connect this phone to Wi-Fi or mobile data.",
				message: "This phone has no internet connection. Workers load once it's back online.",
				showLocalNetworkHint: false,
			};
		}
		if (linkHealth === "reconnecting") {
			const name = selectedHostName?.trim() || "your machine";
			return {
				title: "Reconnecting…",
				icon: "refresh-cw",
				hint: `Still trying to reach ${name}.`,
				message: `Still trying to reach ${name}. This usually clears on its own.`,
				showLocalNetworkHint: false,
			};
		}
		const classified = classifyConnectionFailure(errorStatus ?? undefined);
		return describeConnectionFailure(
			// A stored tunnel that no longer answers means the hostname rotated,
			// which no amount of retrying fixes — rescanning does. Distinguished
			// here rather than in the classifier because it depends on what the
			// machine advertised, not on a status code.
			classified === "unreachable" && tunnelMayHaveRotated(activeEndpoints, config?.endpointKind, connection === "open")
				? "tunnel-rotated"
				: classified,
			{ host: config?.host ?? "", port: config?.httpPort ?? "", platform: Platform.OS },
		);
	}, [errorStatus, config?.host, config?.httpPort, activeEndpoints, connection, linkHealth, selectedHostName]);
}
