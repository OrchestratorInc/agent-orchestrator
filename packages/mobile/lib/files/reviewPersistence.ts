import AsyncStorage from "@react-native-async-storage/async-storage";
import { AppState } from "react-native";
import { onReviewsChanged, restoreReviews, storedReviews } from "./reviewStore";

// Saves review comments to the phone and puts them back on launch, so a review
// survives the phone closing the app in the background. Kept apart from the
// store, which stays free of native modules and so testable on its own.

export const REVIEWS_STORAGE_KEY = "ao.review.comments.v1";
const WRITE_DELAY_MS = 300;

let started = false;

/** Restores saved reviews, then saves every change. Once per app launch. */
export async function startReviewPersistence(): Promise<void> {
	if (started) return;
	started = true;
	try {
		const raw = await AsyncStorage.getItem(REVIEWS_STORAGE_KEY);
		if (raw) restoreReviews(JSON.parse(raw));
	} catch {
		// Unreadable storage must not keep the app from reviewing; it is overwritten below.
	}
	let timer: ReturnType<typeof setTimeout> | undefined;
	let writing = Promise.resolve();
	const write = () => {
		clearTimeout(timer);
		timer = undefined;
		const snapshot = storedReviews();
		// One write at a time, in order, so an older snapshot never lands last.
		writing = writing.then(() => snapshot.reviews.length
			? AsyncStorage.setItem(REVIEWS_STORAGE_KEY, JSON.stringify(snapshot))
			: AsyncStorage.removeItem(REVIEWS_STORAGE_KEY)).catch(() => {});
	};
	onReviewsChanged(() => {
		clearTimeout(timer);
		timer = setTimeout(write, WRITE_DELAY_MS);
	});
	// Leaving the app is when the phone may close it: a change still waiting is written now.
	AppState.addEventListener("change", (state) => {
		if (state !== "active" && timer) write();
	});
}
