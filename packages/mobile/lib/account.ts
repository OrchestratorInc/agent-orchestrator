import { CLOUD_DEPLOYMENT } from "./cloud/config";
import { getAccountSessionOwner, signInToDeployment } from "./cloud/sessionOwner";
import { cancelAccountHostSync } from "./accountHosts";

export type Account = { id: string; email: string };

/** Both account-host discovery and Cloud sessions share one credential owner. */
export async function loadAccount(): Promise<Account | null> {
	const owner = getAccountSessionOwner(CLOUD_DEPLOYMENT);
	await owner.initialize();
	return owner.getSnapshot().account;
}

export async function signInToAccount(): Promise<Account | null> {
	return await signInToDeployment(CLOUD_DEPLOYMENT) ? getAccountSessionOwner(CLOUD_DEPLOYMENT).getSnapshot().account : null;
}

export async function signOutOfAccount(): Promise<void> {
	cancelAccountHostSync();
	await getAccountSessionOwner(CLOUD_DEPLOYMENT).signOut();
}

/** Account APIs only: never send this bearer to a paired daemon. */
export function getAccountAccessToken(): Promise<string | null> {
	return getAccountSessionOwner(CLOUD_DEPLOYMENT).getToken();
}
