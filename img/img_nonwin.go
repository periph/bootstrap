// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

//go:build !windows
// +build !windows

package img

func flashWindows(imgPath, disk string) error {
	return nil
}

func mountWindows(disk string, n int) (string, error) {
	return "", nil
}

func umountWindows(disk string) error {
	return nil
}

func listSDCardsWindows() []string {
	return nil
}
