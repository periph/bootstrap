// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// Unavailable Windows disk operations on other operating systems.

//go:build !windows

package img

import "context"

func flashWindows(_ context.Context, _, _ string) error {
	return nil
}

func mountWindows(_ string, _ int) (string, error) {
	return "", nil
}

func umountWindows(_ string) error {
	return nil
}

func listSDCardsWindows(_ context.Context) []string {
	return nil
}
