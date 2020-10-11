// Copyright 2020 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package img

import "testing"

func TestUdisksctlMount(t *testing.T) {
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
