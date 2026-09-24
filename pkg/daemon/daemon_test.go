package daemon

import (
	"net"
	"os"
	"testing"
)

func TestRemoveStaleSocket(t *testing.T) {
	dir := t.TempDir()

	// Nonexistent path is a no-op.
	if err := removeStaleSocket(dir + "/none.sock"); err != nil {
		t.Fatalf("missing socket should be ok, got %v", err)
	}

	// A non-socket file is refused, not deleted.
	plain := dir + "/plain"
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleSocket(plain); err == nil {
		t.Fatal("must refuse to remove a non-socket file")
	}
	if _, err := os.Stat(plain); err != nil {
		t.Fatal("non-socket file must be left intact")
	}

	// A socket with a live listener is refused (don't clobber a running daemon).
	live := dir + "/live.sock"
	ll, err := net.Listen("unix", live)
	if err != nil {
		t.Fatal(err)
	}
	defer ll.Close()
	if err := removeStaleSocket(live); err == nil {
		t.Fatal("must refuse to remove a socket with a live listener")
	}

	// A stale socket file (no listener) is removed. SetUnlinkOnClose(false)
	// leaves the file behind on Close, emulating a SIGKILLed daemon.
	stale := dir + "/stale.sock"
	sl, err := net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	sl.SetUnlinkOnClose(false)
	_ = sl.Close()
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("precondition: stale socket file should exist, got %v", err)
	}
	if err := removeStaleSocket(stale); err != nil {
		t.Fatalf("stale socket should be removed, got %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale socket file should be gone")
	}
}
