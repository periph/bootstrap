// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import "testing"

func TestWPAPSK(t *testing.T) {
	// Generated with:
	// wpa_passphrase "the ssid" "long passphrase"
	expected := "ae1b388ef471b4b65cf8d0b6cd3720e7ee7074f77e31061121ac8894973642c5"
	if actual := wpaPSK("long passphrase", "the ssid"); actual != expected {
		t.Fatal(actual)
	}
}
