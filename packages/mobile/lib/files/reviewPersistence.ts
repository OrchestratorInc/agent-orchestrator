import AsyncStorage from "@react-native-async-storage/async-storage";
import { AppState } from "react-native";
import { beginRestoringReviews, finishRestoringReviews, onReviewsChanged, restoreReviews, storedReviews } from "./reviewStore";

// Saves review comments to the phone and puts them back on launch, so a review
// survives the phone closing the app in the background. Kept apart from the
// store, which stays free of native modules and so testable on its own.

export const REVIEWS_STORAGE_KEY = "ao.review.comments.v1";
const WRITE_DELAY_MS = 300;
/** A save that fails is tried again this many times, each after twice the wait of the last. */
const WRITE_RETRIES = 3;

let started = false;
let loaded = false;
// A change not yet on the phone: a save waiting, or one that failed.
let dirty = false;
let failures = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
let writing: Promise<void> = Promise.resolve();
// The latest write as it went: rejects if it failed (`writing` never does, so
// the next write still runs).
let lastWrite: Promise<void> = Promise.resolve();
// Settles once the saved reviews were read back at launch and what changed
// meanwhile is scheduled; null until startReviewPersistence runs.
let ready: Promise<void> | null = null;

function schedule(delayMs = WRITE_DELAY_MS) {
	dirty = true;
	clearTimeout(timer);
	timer = setTimeout(() => void write().catch(() => {}), delayMs);
}

/** Writes what is in memory now, after the writes before it; rejects if it did not reach the phone. */
function write(): Promise<void> {
	clearTimeout(timer);
	timer = undefined;
	dirty = false;
	const snapshot = storedReviews();
	// One write at a time, in order, so an older snapshot never lands last.
	const result = writing.then(() => snapshot.reviews.length
		? AsyncStorage.setItem(REVIEWS_STORAGE_KEY, JSON.stringify(snapshot))
		: AsyncStorage.removeItem(REVIEWS_STORAGE_KEY));
	lastWrite = result;
	writing = result.then(
		() => { failures = 0; },
		(error: unknown) => {
			// Still not on the phone: kept to save again, unless a newer change
			// already will. Tried again shortly a few times, then on leaving the app.
			if (dirty) return;
			dirty = true;
			failures++;
			if (failures <= WRITE_RETRIES) schedule(WRITE_DELAY_MS * 2 ** failures);
			else console.warn("[reviews] saving review comments keeps failing", error);
		},
	);
	return result;
}

/**
 * Writes a change still waiting (or a save that failed) now and resolves once
 * every save started so far is done; rejects if what is in memory still did
 * not reach the phone, a save already running included. While the saved
 * reviews are still being read back, waits for that read first, so a change
 * made meanwhile (a forgotten machine's reviews removed) is written before
 * this resolves. Before persistence started there is nothing to write.
 */
export async function flushReviewPersistence(): Promise<void> {
	if (!loaded) {
		if (!ready) return;
		await ready;
	}
	if (timer || dirty) return write();
	await lastWrite;
}

/** Restores saved reviews, then saves every change. Once per app launch. */
export function startReviewPersistence(): Promise<void> {
	if (!ready) ready = startOnce();
	return ready;
}

async function startOnce(): Promise<void> {
	if (started) return;
	started = true;
	// Listening starts before the saved reviews are read: a comment written
	// while that read is pending is saved once it lands. Not before: a write
	// then would replace the saved reviews with the new comment alone.
	let changedWhileLoading = false;
	let restoring = false;
	onReviewsChanged(() => {
		if (restoring) return;
		if (loaded) schedule();
		else changedWhileLoading = true;
	});
	beginRestoringReviews();
	try {
		const raw = await AsyncStorage.getItem(REVIEWS_STORAGE_KEY);
		restoring = true;
		if (raw) restoreReviews(JSON.parse(raw));
	} catch {
		// Unreadable storage must not keep the app from reviewing; it is overwritten below.
	} finally {
		finishRestoringReviews();
		restoring = false;
		loaded = true;
	}
	// What was written meanwhile, with the restored reviews before it. Out of
	// the foreground already, the leaving was missed (nothing listened yet), so
	// it is written now rather than after a delay the phone may never allow.
	if (changedWhileLoading) {
		if (AppState.currentState !== "active") void write().catch(() => {});
		else schedule();
	}
	// Leaving the app is when the phone may close it: a change still waiting, or a save that failed, is written now.
	AppState.addEventListener("change", (state) => {
		if (state !== "active" && (timer || dirty)) void write().catch(() => {});
	});
}
