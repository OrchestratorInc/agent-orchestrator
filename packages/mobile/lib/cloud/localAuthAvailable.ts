/**
 * Whether the email/password form can possibly work against this control plane.
 *
 * Local auth is a development affordance: `cloud/compose.yaml` sets
 * AO_CLOUD_LOCAL_AUTH for the Docker stack, and
 * cloud/internal/config/config.go refuses to enable it in staging or
 * production. Offering the form against a hosted URL only produces
 * "Local authentication is disabled", so it is hidden there.
 *
 * Plain http is the signal: a developer stack runs over http on loopback or a
 * LAN address, while hosted deployments are https.
 */
export function localAuthAvailable(baseUrl: string): boolean {
	try {
		return new URL(baseUrl).protocol === "http:";
	} catch {
		return false;
	}
}
