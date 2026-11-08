// Copyright 2020 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// Tests image downloads, cancellation, mount parsing, and flash preparation.

package img

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"runtime"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestFlashUnmountError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the invalid glob error is specific to Linux")
	}
	if err := Flash(t.Context(), "unused", "["); err == nil {
		t.Fatal("Flash must report the unmount error")
	}
}

func TestUdisksctlMount(t *testing.T) {
	t.Parallel()
	data := []string{
		"Mounted /dev/sdh2 at /media/<user>/<GUID>.\n",
		"Mounted /dev/sdh2 at /media/<user>/<GUID>\n",
		"Error mounting /dev/sdh2: GDBus.Error:org.freedesktop.UDisks2.Error.AlreadyMounted: Device /dev/sdh2 is already mounted at `/media/<user>/<GUID>'",
	}
	for _, in := range data {
		if dst := udisksctlMount(in); dst != "/media/<user>/<GUID>" {
			t.Fatalf("%q: %s", in, dst)
		}
	}
}

func TestFetchURLCancellation(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := fetchURL(ctx, srv.URL)
		result <- err
	}()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("fetchURL error = %v, want cancellation", err)
	}
}

func TestFetchXZIncompletePreservesImage(t *testing.T) {
	t.Parallel()
	var data bytes.Buffer
	w, err := xz.NewWriter(&data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("image content")); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	partial := data.Bytes()[:data.Len()-8]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(partial)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	path := filepath.Join(dir, "image.img")
	if err = os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = fetchXZ(t.Context(), srv.URL, path); err == nil {
		t.Fatal("expected truncated image to fail")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "existing" {
		t.Fatalf("image = %q, error = %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("directory entries = %v, error = %v", entries, err)
	}
}
