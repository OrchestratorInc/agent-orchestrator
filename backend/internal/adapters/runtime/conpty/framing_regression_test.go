package conpty

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestWatchedClientDropReleasesForegroundCount covers every path that removes
// a client from the broadcast set. A dropped foreground watcher that kept its
// count left fgWatchers above zero forever, so record kept feeding the tracker
// (one processArgv read per evaluation) with nobody watching.
func TestWatchedClientDropReleasesForegroundCount(t *testing.T) {
	// newWatchedHost returns a host holding one foreground watcher on hostEnd.
	newWatchedHost := func(t *testing.T, hostEnd net.Conn) *host {
		t.Helper()
		h := &host{clients: map[net.Conn]*clientState{}, shutdownC: make(chan struct{})}
		client := newClientState()
		client.watcher = true
		h.clients[hostEnd] = client
		h.fgWatchers.Add(1)
		return h
	}

	t.Run("sendToDropsAnUnreachableClient", func(t *testing.T) {
		hostEnd, peer := net.Pipe()
		defer func() { _ = peer.Close() }()
		h := newWatchedHost(t, hostEnd)

		// A closed done channel makes enqueue fail, which is the drop trigger.
		h.clients[hostEnd].closeOnce.Do(func() { close(h.clients[hostEnd].done) })
		h.sendTo(hostEnd, []byte{MsgForegroundState})

		if _, attached := h.clients[hostEnd]; attached {
			t.Fatal("an unreachable client must be dropped")
		}
		if got := h.fgWatchers.Load(); got != 0 {
			t.Fatalf("fgWatchers = %d after dropping a watcher, want 0", got)
		}
	})

	t.Run("lockWithRoomDropsAStalledWatcher", func(t *testing.T) {
		defer func(grace time.Duration) { slowClientGrace = grace }(slowClientGrace)
		slowClientGrace = 50 * time.Millisecond

		hostEnd, peer := net.Pipe()
		defer func() { _ = peer.Close() }()
		h := newWatchedHost(t, hostEnd)
		// No writer and a full queue: nothing ever frees space, so the grace
		// deadline is the only way out.
		client := h.clients[hostEnd]
		for client.hasRoom() {
			client.out <- []byte{MsgTerminalData}
		}

		// After the grace deadline it drops the stalled viewer, rescans, and
		// hands the lock to the read loop because no queue is full any more.
		if !h.lockWithRoom() {
			t.Fatal("lockWithRoom must succeed once the stalled viewer is dropped")
		}
		h.mu.Unlock()
		if _, attached := h.clients[hostEnd]; attached {
			t.Fatal("a stalled watcher must be dropped")
		}
		if got := h.fgWatchers.Load(); got != 0 {
			t.Fatalf("fgWatchers = %d after dropping a stalled watcher, want 0", got)
		}
	})

	t.Run("setTitleDropsAFullQueue", func(t *testing.T) {
		hostEnd, peer := net.Pipe()
		defer func() { _ = peer.Close() }()
		h := newWatchedHost(t, hostEnd)
		client := h.clients[hostEnd]
		client.titleWatcher = true
		for client.hasRoom() {
			client.out <- []byte{MsgTerminalData}
		}

		h.setTitle("building")

		if _, attached := h.clients[hostEnd]; attached {
			t.Fatal("a watcher whose queue is full must be dropped")
		}
		if got := h.fgWatchers.Load(); got != 0 {
			t.Fatalf("fgWatchers = %d after dropping a full-queue watcher, want 0", got)
		}
	})

	t.Run("removeClientReleasesTheCountOnce", func(t *testing.T) {
		hostEnd, peer := net.Pipe()
		defer func() { _ = peer.Close() }()
		h := newWatchedHost(t, hostEnd)

		// Both the reader and the writer discover a dead connection, so the
		// release must be idempotent.
		client := h.clients[hostEnd]
		h.removeClient(hostEnd, client)
		h.removeClient(hostEnd, client)

		if got := h.fgWatchers.Load(); got != 0 {
			t.Fatalf("fgWatchers = %d after a repeated removal, want 0", got)
		}
	})
}

// TestParserRejectsOversizedFrame: the 4-byte length comes from the peer, and
// the parser must reject a length above the cap instead of waiting for and then
// allocating that payload. A declared length near 2^32 also overflows 5+int(n)
// on a 32-bit build, which used to pass the "whole frame buffered" check.
func TestParserRejectsOversizedFrame(t *testing.T) {
	for _, size := range []uint32{MaxFramePayload + 1, ^uint32(0)} {
		var got [][]byte
		p := NewMessageParser(func(_ byte, payload []byte) { got = append(got, payload) })

		header := make([]byte, 5)
		header[0] = MsgTerminalData
		binary.BigEndian.PutUint32(header[1:5], size)
		p.Feed(header)
		// More chunks must not revive a parser whose stream desynchronized.
		p.Feed([]byte{MsgTerminalData, 0, 0, 0, 1, 'x'})

		if len(got) != 0 {
			t.Fatalf("length %d: parser dispatched %d frames, want 0", size, len(got))
		}
		if !errors.Is(p.Err(), ErrFrameTooLarge) {
			t.Fatalf("length %d: Err() = %v, want ErrFrameTooLarge", size, p.Err())
		}
	}
}

// TestEncodeMessageRejectsPayloadAboveParserLimit keeps the two bounds equal:
// the host must never emit a frame its own parser rejects.
func TestEncodeMessageRejectsPayloadAboveParserLimit(t *testing.T) {
	if _, err := EncodeMessage(MsgTerminalData, make([]byte, MaxFramePayload+1)); err == nil {
		t.Fatal("EncodeMessage accepted a payload the parser rejects")
	}
	if _, err := EncodeMessage(MsgTerminalData, make([]byte, MaxFramePayload)); err != nil {
		t.Fatalf("EncodeMessage rejected the largest payload the parser accepts: %v", err)
	}
}

// TestSendMessageStopsOnCancel: SendMessage must stop typing when its caller
// gives up. The inter-chunk and pre-Enter pauses used to be plain sleeps, so a
// cancelled request still finished the whole paste.
func TestSendMessageStopsOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, conn) // read frames, never reply
		_ = conn.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	// Long enough to span several chunks, so the paste is still in progress.
	// Its pre-Enter pause is inputdelay's burst delay (about a second), so a
	// cancelled call that kept sleeping would far exceed the bound below.
	message := strings.Repeat("a", ptyInputChunkRunes+1)
	errC := make(chan error, 1)
	go func() { errC <- clientSendMessage(ctx, ln.Addr().String(), message) }()

	time.Sleep(2 * ptyInputChunkDelay)
	cancelled := time.Now()
	cancel()

	select {
	case err := <-errC:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("clientSendMessage error = %v, want context.Canceled", err)
		}
		if waited := time.Since(cancelled); waited > 250*time.Millisecond {
			t.Fatalf("clientSendMessage returned %v after cancellation; it kept sleeping through the paste", waited)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clientSendMessage ignored cancellation and kept typing")
	}
}
