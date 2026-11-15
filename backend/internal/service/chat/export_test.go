package chat

import "time"

func SetNativeHistoryLoadAttemptLimit(limit time.Duration) (restore func()) {
	previous := nativeHistoryLoadAttemptLimit
	nativeHistoryLoadAttemptLimit = limit
	return func() { nativeHistoryLoadAttemptLimit = previous }
}

func SetRenderMeasureTimeout(timeout time.Duration) (restore func()) {
	previous := renderMeasureTimeout
	renderMeasureTimeout = timeout
	return func() { renderMeasureTimeout = previous }
}

func (s *Service) WaitRenderMeasures() { s.renderMeasures.Wait() }

func SetDeltaFlushInterval(interval time.Duration) (restore func()) {
	previous := deltaFlushInterval
	deltaFlushInterval = interval
	return func() { deltaFlushInterval = previous }
}

const DeltaRunMaxBytes = deltaRunMaxBytes
