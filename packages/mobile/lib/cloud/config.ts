import { resolveCloudDeployment } from "./deployment";

function configuredDeployment() {
	try {
		return { deployment: resolveCloudDeployment({
			cloudUrl: process.env.EXPO_PUBLIC_AO_CLOUD_URL,
			accountUrl: process.env.EXPO_PUBLIC_AO_CLOUD_API_BASE_URL,
			clientId: process.env.EXPO_PUBLIC_WORKOS_CLIENT_ID,
			development: typeof __DEV__ !== "undefined" && __DEV__,
		}), error: null };
	} catch (cause) {
		// Keep pairing usable while refusing requests to either conflicting API.
		return { deployment: resolveCloudDeployment({ cloudUrl: "https://configuration.invalid", development: false }),
			error: cause instanceof Error ? cause.message : "Invalid Cloud deployment settings." };
	}
}

const configured = configuredDeployment();
export const CLOUD_DEPLOYMENT = configured.deployment;
export const CLOUD_CONFIGURATION_ERROR = configured.error;
export const CLOUD_BASE_URL = CLOUD_DEPLOYMENT.baseUrl;
export const WORKOS_CLIENT_ID = CLOUD_DEPLOYMENT.clientId;
export const WORKOS_REDIRECT_URI = CLOUD_DEPLOYMENT.redirectUri;
export const CLOUD_API_PREFIX = "/api/cloud/v1";
