import { useWarmCloudModelCatalogs } from "../hooks/useCloudModelCatalog";

/** Holds every logged-in Cloud harness's model list for the whole app (useWarmCloudModelCatalogs); renders nothing. */
export function CloudModelCatalogWarmer() {
	useWarmCloudModelCatalogs();
	return null;
}
