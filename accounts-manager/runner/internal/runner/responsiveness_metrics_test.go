//go:build performance

package runner

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

type responseMeasurement struct {
	FirstMS    float64 `json:"firstEventMs"`
	CompleteMS float64 `json:"completeMs"`
}

type measurementDistribution struct {
	Count int     `json:"count"`
	Min   float64 `json:"minMs"`
	P50   float64 `json:"p50Ms"`
	P95   float64 `json:"p95Ms"`
	Max   float64 `json:"maxMs"`
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func measuredDistribution(values []float64) measurementDistribution {
	if len(values) == 0 {
		return measurementDistribution{}
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	return measurementDistribution{
		Count: len(ordered), Min: ordered[0], Max: ordered[len(ordered)-1],
		P50: ordered[int(math.Ceil(float64(len(ordered))*0.50))-1],
		P95: ordered[int(math.Ceil(float64(len(ordered))*0.95))-1],
	}
}

func readMeasuredStream(response *http.Response, started time.Time, now func() time.Time) (responseMeasurement, error) {
	var result responseMeasurement
	if response.StatusCode != http.StatusOK {
		return result, errors.New("stream request rejected")
	}
	first, complete := false, false
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			continue
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(data), &event) != nil || event.Type == "" {
			return result, errors.New("invalid stream event")
		}
		if !first && event.Type == "response.output_text.delta" {
			result.FirstMS, first = milliseconds(now().Sub(started)), true
		}
		if event.Type == "error" || event.Type == "response.failed" {
			return result, errors.New("stream reported failure")
		}
		if event.Type == "response.completed" {
			complete = true
		}
	}
	if scanner.Err() != nil || !first || !complete {
		return result, errors.New("stream did not complete")
	}
	result.CompleteMS = milliseconds(now().Sub(started))
	return result, nil
}

type observationReader struct {
	chunks []string
	step   int
	onRead func(int)
}

func (r *observationReader) Read(buffer []byte) (int, error) {
	if r.step == len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(buffer, r.chunks[r.step])
	r.chunks[r.step] = r.chunks[r.step][n:]
	if r.chunks[r.step] == "" {
		r.onRead(r.step)
		r.step++
	}
	return n, nil
}

func TestRunnerMeasurementOracle(t *testing.T) {
	t.Run("data arrival not headers", func(t *testing.T) {
		started := time.Unix(100, 0)
		observed := started
		reader := &observationReader{
			chunks: []string{": heartbeat\n\ndata: {\"type\":\"response.created\"}\n\n", "data: {\"type\":\"response.output_text.delta\"}\n\n", "data: {\"type\":\"response.completed\"}\n\n"},
			onRead: func(step int) { observed = started.Add([]time.Duration{5, 20, 50}[step] * time.Millisecond) },
		}
		result, err := readMeasuredStream(&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(reader)}, started, func() time.Time { return observed })
		if err != nil || result.FirstMS != 20 || result.CompleteMS != 50 {
			t.Fatalf("measurement=%+v err=%v", result, err)
		}
	})
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"rejected", 401, ""},
		{"empty", 200, ""},
		{"headers and heartbeat only", 200, ": heartbeat\n\n"},
		{"truncated", 200, "data: {\"type\":\"response.output_text.delta\"}\n\n"},
		{"malformed", 200, "data: {\n\n"},
		{"untyped", 200, "data: {}\n\n"},
		{"failure", 200, "data: {\"type\":\"response.failed\"}\n\n"},
		{"completion without upstream data", 200, "data: {\"type\":\"response.completed\"}\n\n"},
		{"metadata without upstream data", 200, "data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"},
		{"error after completion", 200, "data: {\"type\":\"response.completed\"}\n\ndata: {\"type\":\"error\"}\n\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body))}
			if _, err := readMeasuredStream(response, time.Now(), time.Now); err == nil {
				t.Fatal("invalid response counted as a sample")
			}
		})
	}
	t.Run("nearest rank and signed deltas", func(t *testing.T) {
		values := []float64{100, -3, 4, 2, 1}
		got := measuredDistribution(values)
		if got.Count != 5 || got.Min != -3 || got.P50 != 2 || got.P95 != 100 || got.Max != 100 || values[0] != 100 {
			t.Fatalf("distribution=%+v input=%v", got, values)
		}
		if measuredDistribution(nil).Count != 0 {
			t.Fatal("empty input invented a sample")
		}
	})
}
