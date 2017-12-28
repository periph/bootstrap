// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package img

import (
	"errors"
	"strings"
)

func flashWindows(imgPath, disk string) error {
	return errors.New("Flash() is not implemented on Windows")
}

func mountWindows(disk string, n int) (string, error) {
	return "", errors.New("Mount() is not implemented on Windows")
}

func umountWindows(disk string) error {
	return errors.New("Umount() is not implemented on Windows")
}

func listSDCardsWindows() []string {
	var out []string
	for _, disk := range wmicListDisks() {
		if disk["MediaLoaded"] == "TRUE" && disk["MediaType"] == "Removable Media" {
			out = append(out, disk["DeviceID"])
		}
	}
	return out
}

func wmicListDisks() []map[string]string {
	// TODO(maruel): It'd be nicer to use the Win32 APIs but shelling out will be
	// good enough for now. Shelling out permits to not have to do COM/WMI.
	// In theory, /format:xml would produce an output meant to be mechanically
	// parsed, in practice the output is suboptimal.
	b, err := capture("", "wmic", "diskdrive", "list")
	if err != nil || len(b) == 0 {
		return nil
	}
	s := strings.Replace(string(b), "\r", "", -1)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	type col struct {
		start, end int
		name       string
	}
	var cols []col
	l := lines[0]
	for i := 0; i < len(l); {
		end := strings.IndexByte(l[i:], ' ')
		if end == -1 {
			// Last column.
			end = len(l)
		} else {
			for end += i; end < len(l) && l[end] == ' '; end++ {
			}
		}
		name := strings.TrimSpace(l[i:end])
		if name == "" {
			break
		}
		cols = append(cols, col{i, end, name})
		i = end
	}
	var out []map[string]string
	for i := 1; i < len(lines); i++ {
		m := map[string]string{}
		for _, c := range cols {
			m[c.name] = strings.TrimSpace(lines[i][c.start:c.end])
		}
		out = append(out, m)
	}
	return out
}
