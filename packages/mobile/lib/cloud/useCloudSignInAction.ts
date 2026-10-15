import { useRouter } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { Alert } from "react-native";
import { useCloudAuth } from "./authStore";
import { CLOUD_BASE_URL } from "./config";
import { localAuthAvailable } from "./localAuthAvailable";
import { beginCloudSignIn } from "./signInAction";

type LocalAuthRoute = "/sheets/cloud-signin" | "/sheets/cloud-signin?from=onboarding";

/** Launch production AuthKit directly, retaining the native form for local development. */
export function useCloudSignInAction(localRoute: LocalAuthRoute = "/sheets/cloud-signin") {
	const router = useRouter();
	const cloudAuth = useCloudAuth();
	const mounted = useRef(true);
	useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
	const inFlight = useRef(false);
	const [busy, setBusy] = useState(false);

	const signIn = useCallback(async (): Promise<boolean> => {
		if (!mounted.current || inFlight.current) return false;
		inFlight.current = true;
		setBusy(true);
		try {
			const result = await beginCloudSignIn({
				localAuth: localAuthAvailable(CLOUD_BASE_URL),
				openLocalAuth: () => router.push(localRoute),
				signInWithWorkOS: cloudAuth.signInWithWorkOS,
			});
			if (!mounted.current) return false;
			if (result.status === "failed") {
				Alert.alert("Couldn’t sign in", result.message);
			}
			return result.status === "authenticated";
		} finally {
			inFlight.current = false;
			if (mounted.current) setBusy(false);
		}
	}, [cloudAuth.signInWithWorkOS, localRoute, router]);

	return { signIn, busy };
}
