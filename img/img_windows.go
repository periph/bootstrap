// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package img

import (
	"errors"
)

func flashWindows(imgPath, disk string) error {
	return errors.New("Flash() is not implemented on Windows")
}

func mountWindows(disk string, n int) (string, error) {
	return errors.New("Mount() is not implemented on Windows")
}

func umountWindows(disk string) error {
	return errors.New("Umount() is not implemented on Windows")
}

func listSDCardsWindows() []string {
	return nil
}
