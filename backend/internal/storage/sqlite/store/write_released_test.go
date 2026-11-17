package store_test

import (
	"context"
	"testing"
)

func TestWriteSignalsWriteReleased(t *testing.T) {
	s := newTestStore(t)
	for len(s.WriteReleased()) > 0 {
		<-s.WriteReleased()
	}
	seedProject(t, s, "mer")
	if _, err := s.CreateSession(context.Background(), sampleRecord("mer")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.WriteReleased():
	default:
		t.Fatal("no signal after a committed write")
	}
}
