// Reports when fingers touch and leave the trackpad during a scroll gesture.
// DOM wheel events carry no phase, so without this the renderer cannot tell a
// swipe that is being held from one that was released, and momentum scrolling
// after the lift looks like more swiping. A local NSEvent monitor sees every
// scroll event this app receives, before Chromium consumes it.
#import <AppKit/AppKit.h>

#define NAPI_VERSION 8
#include <node_api.h>

static napi_threadsafe_function phaseCallback = nullptr;
static id monitor = nil;

static const intptr_t kFingersDown = 1;
static const intptr_t kFingersUp = 2;
static const intptr_t kCancelled = 3;

static void CallJs(napi_env env, napi_value callback, void*, void* data) {
	if (env == nullptr || callback == nullptr) return;
	napi_value phase;
	napi_value undefined;
	const intptr_t report = reinterpret_cast<intptr_t>(data);
	const char* name = report == kFingersDown ? "down" : report == kFingersUp ? "up" : "cancel";
	napi_create_string_utf8(env, name, NAPI_AUTO_LENGTH, &phase);
	napi_get_undefined(env, &undefined);
	napi_call_function(env, undefined, callback, 1, &phase, nullptr);
}

// Runs on the main thread when Node tears down the callback at quit. AppKit can
// still dispatch scroll events after that, so the monitor must go with it.
static void Finalize(napi_env, void*, void*) {
	if (monitor != nil) [NSEvent removeMonitor:monitor];
	monitor = nil;
	phaseCallback = nullptr;
}

// start(callback): calls callback("down" | "up" | "cancel") for each trackpad gesture.
static napi_value Start(napi_env env, napi_callback_info info) {
	size_t argc = 1;
	napi_value callback = nullptr;
	napi_get_cb_info(env, info, &argc, &callback, nullptr, nullptr);
	if (phaseCallback != nullptr || argc < 1) return nullptr;

	napi_value name;
	napi_create_string_utf8(env, "aoScrollPhase", NAPI_AUTO_LENGTH, &name);
	if (napi_create_threadsafe_function(env, callback, nullptr, name, 0, 1, nullptr, Finalize, nullptr, CallJs, &phaseCallback) != napi_ok) {
		napi_throw_error(env, nullptr, "failed to create scroll phase callback");
		return nullptr;
	}
	// The monitor lives as long as the app; it must not keep Node alive on quit.
	napi_unref_threadsafe_function(env, phaseCallback);

	monitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskScrollWheel
	                                              handler:^NSEvent*(NSEvent* event) {
		// Momentum events report through momentumPhase with phase == None, so
		// only the finger-driven part of the gesture reaches here.
		const NSEventPhase phase = event.phase;
		intptr_t report = 0;
		if (phase & (NSEventPhaseMayBegin | NSEventPhaseBegan)) report = kFingersDown;
		else if (phase & NSEventPhaseCancelled) report = kCancelled;
		else if (phase & NSEventPhaseEnded) report = kFingersUp;
		if (report != 0 && phaseCallback != nullptr) {
			napi_call_threadsafe_function(phaseCallback, reinterpret_cast<void*>(report), napi_tsfn_nonblocking);
		}
		return event;
	}];
	return nullptr;
}

static napi_value Init(napi_env env, napi_value exports) {
	napi_value start;
	napi_create_function(env, "start", NAPI_AUTO_LENGTH, Start, nullptr, &start);
	napi_set_named_property(env, exports, "start", start);
	return exports;
}

NAPI_MODULE(ao_scroll_phase, Init)
