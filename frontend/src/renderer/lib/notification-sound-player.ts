import notificationSoundUrl from "../assets/notification.mp3";

export function playNotificationSound(onFailure: () => void): void {
	if (typeof Audio === "undefined") {
		onFailure();
		return;
	}
	const audio = new Audio(notificationSoundUrl);
	let failed = false;
	const fail = () => {
		if (failed) return;
		failed = true;
		onFailure();
	};
	audio.addEventListener("error", fail, { once: true });
	audio.play().catch(fail);
}
