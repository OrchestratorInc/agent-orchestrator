import AVFoundation
import Foundation
import Speech

// Streams one dictation as JSON lines on stdout. "stop" on stdin (or EOF)
// ends the audio and waits for the final transcript; "cancel" exits at once.

let output = DispatchQueue(label: "ao.dictation.output")

func emit(_ event: [String: Any]) {
	guard let data = try? JSONSerialization.data(withJSONObject: event) else { return }
	output.async {
		FileHandle.standardOutput.write(data)
		FileHandle.standardOutput.write(Data([0x0A]))
	}
}

func finish(_ event: [String: Any], code: Int32) -> Never {
	emit(event)
	output.sync {}
	exit(code)
}

func fail(_ message: String) -> Never {
	finish(["type": "error", "message": message], code: 1)
}

// RMS of the first channel mapped from -50...0 dBFS onto 0...1.
func level(_ buffer: AVAudioPCMBuffer) -> Double {
	guard let samples = buffer.floatChannelData?[0], buffer.frameLength > 0 else { return 0 }
	var sum: Float = 0
	for index in 0..<Int(buffer.frameLength) { sum += samples[index] * samples[index] }
	let decibels = 20 * log10(max(sqrt(sum / Float(buffer.frameLength)), 1e-6))
	return Double(min(max((decibels + 50) / 50, 0), 1))
}

final class Dictation {
	private let engine = AVAudioEngine()
	private let request = SFSpeechAudioBufferRecognitionRequest()
	private var task: SFSpeechRecognitionTask?
	private var text = ""
	private var stopping = false

	func start() {
		guard let recognizer = SFSpeechRecognizer(locale: Locale.current) ?? SFSpeechRecognizer(), recognizer.isAvailable else {
			fail("Speech recognition is not available for your language.")
		}
		request.shouldReportPartialResults = true
		if #available(macOS 13, *) { request.addsPunctuation = true }
		if recognizer.supportsOnDeviceRecognition { request.requiresOnDeviceRecognition = true }

		let input = engine.inputNode
		let format = input.outputFormat(forBus: 0)
		guard format.sampleRate > 0, format.channelCount > 0 else { fail("No microphone found.") }
		let request = self.request
		var lastLevel = Date.distantPast
		input.installTap(onBus: 0, bufferSize: 1024, format: format) { buffer, _ in
			request.append(buffer)
			let now = Date()
			if now.timeIntervalSince(lastLevel) >= 0.05 {
				lastLevel = now
				emit(["type": "level", "value": level(buffer)])
			}
		}

		task = recognizer.recognitionTask(with: request) { [weak self] result, error in
			guard let self else { return }
			if let result {
				self.text = result.bestTranscription.formattedString
				if result.isFinal { finish(["type": "end", "text": self.text], code: 0) }
				emit(["type": "text", "text": self.text])
			} else if error != nil {
				// Stopping with no speech also ends here; keep whatever was heard.
				if self.stopping { finish(["type": "end", "text": self.text], code: 0) }
				fail(error?.localizedDescription ?? "Dictation stopped.")
			}
		}

		engine.prepare()
		do { try engine.start() } catch { fail("Could not start the microphone.") }
		emit(["type": "listening"])
	}

	func stop() {
		guard !stopping else { return }
		stopping = true
		engine.stop()
		engine.inputNode.removeTap(onBus: 0)
		request.endAudio()
		DispatchQueue.main.asyncAfter(deadline: .now() + 3) { [weak self] in
			finish(["type": "end", "text": self?.text ?? ""], code: 0)
		}
	}
}

// macOS asks the responsible process (the app or terminal that launched AO)
// for speech and microphone access, and kills the helper when that process
// has no usage strings. Relaunch once so the helper is responsible for itself
// and macOS reads the usage strings embedded in this binary.
@_silgen_name("responsibility_spawnattrs_setdisclaim")
func responsibility_spawnattrs_setdisclaim(_ attrs: UnsafeMutablePointer<posix_spawnattr_t?>, _ disclaim: Int32) -> Int32

var disclaimedChild: pid_t = 0

if getenv("AO_DICTATION_DISCLAIMED") == nil, let executable = Bundle.main.executablePath {
	setenv("AO_DICTATION_DISCLAIMED", "1", 1)
	var attrs: posix_spawnattr_t?
	posix_spawnattr_init(&attrs)
	_ = responsibility_spawnattrs_setdisclaim(&attrs, 1)
	let argv: [UnsafeMutablePointer<CChar>?] = [strdup(executable), nil]
	if posix_spawn(&disclaimedChild, executable, nil, &attrs, argv, environ) == 0 {
		signal(SIGTERM) { _ in
			kill(disclaimedChild, SIGTERM)
			exit(0)
		}
		var status: Int32 = 0
		waitpid(disclaimedChild, &status, 0)
		exit((status & 0x7f) == 0 ? (status >> 8) & 0xff : 1)
	}
}

let dictation = Dictation()

Thread.detachNewThread {
	while let line = readLine() {
		if line == "cancel" { exit(0) }
		if line == "stop" { break }
	}
	DispatchQueue.main.async { dictation.stop() }
}

SFSpeechRecognizer.requestAuthorization { status in
	guard status == .authorized else {
		fail("Allow Speech Recognition for Agent Orchestrator in System Settings > Privacy & Security.")
	}
	AVCaptureDevice.requestAccess(for: .audio) { granted in
		guard granted else {
			fail("Allow Microphone access for Agent Orchestrator in System Settings > Privacy & Security.")
		}
		DispatchQueue.main.async { dictation.start() }
	}
}

dispatchMain()
