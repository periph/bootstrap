// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package img

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// flashWindows flashes the content of imgPath to physical disk 'disk'.
//
// Requires the process to be running as an admin account with an high level
// token.
//
// 'disk' is expected to be of format "\\\\.\\PHYSICALDRIVEn"
func flashWindows(imgPath, disk string) error {
	fi, err := os.Open(imgPath)
	if err != nil {
		return err
	}
	defer fi.Close()
	fd, err := os.OpenFile(disk, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() {
		if fd != nil {
			fd.Close()
		}
	}()
	// Use manual buffer instead of io.Copy() to control buffer size. 1Mb should
	// be a multiple of all common sector sizes, generally 4Kb or 8Kb.
	var b [1024 * 1024]byte
	for {
		n, err := fi.Read(b[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read %s: %v", imgPath, err)
		}
		nw, err := fd.Write(b[:n])
		if err != nil {
			return fmt.Errorf("failed to write %s: %v", disk, err)
		}
		if nw != n {
			return errors.New("buffer underflow")
		}
	}
	err = fd.Close()
	fd = nil
	return err
}

func mountWindows(disk string, n int) (string, error) {
	return "", errors.New("Mount() is not implemented on Windows")
}

func umountWindows(disk string) error {
	return errors.New("Umount() is not implemented on Windows")
}

func listSDCardsWindows() []string {
	var out []string
	// https://msdn.microsoft.com/en-us/library/windows/desktop/aa394132.aspx
	for _, disk := range wmicList("diskdrive", "get", "medialoaded,mediatype,deviceid") {
		// Some USB devices report as fixed media, but we do not care since we
		// target only SDCards.
		if disk["MediaLoaded"] == "TRUE" && disk["MediaType"] == "Removable Media" {
			// String is in the format "\\\\.\\PHYSICALDRIVEn"
			out = append(out, disk["DeviceID"])
		}
	}
	return out
}

func wmicList(args ...string) []map[string]string {
	// TODO(maruel): It'd be nicer to use the Win32 APIs but shelling out will be
	// good enough for now. Shelling out permits to not have to do COM/WMI.
	// In theory, /format:xml would produce an output meant to be mechanically
	// parsed, in practice the output is suboptimal.
	b, err := capture("", "wmic", args...)
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
