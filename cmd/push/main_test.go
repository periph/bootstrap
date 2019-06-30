// Copyright 2019 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import "testing"

func TestString(t *testing.T) {
	if s := none.String(); s != "none" {
		t.Fatal(s)
	}
	if s := rsyncProgress.String(); s != "rsync" {
		t.Fatal(s)
	}
	if s := rsyncOld.String(); s != "rsync" {
		t.Fatal(s)
	}
	if s := pscp.String(); s != "pscp" {
		t.Fatal(s)
	}
	if s := scp.String(); s != "scp" {
		t.Fatal(s)
	}
}
