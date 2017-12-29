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
	"syscall"
)

// FSCTL_LOCK_VOLUME = CTL_CODE(FILE_DEVICE_FILE_SYSTEM,6,METHOD_BUFFERED,FILE_ANY_ACCESS)
const fsctlLockVolume = 0x90018

// FSCTL_DISMOUNT_VOLUME = CTL_CODE(FILE_DEVICE_FILE_SYSTEM,8,METHOD_BUFFERED,FILE_ANY_ACCESS)
const fsctlDismountVolume = 0x90020

// IOCTL_DISK_UPDATE_PROPERTIES = CTL_CODE(IOCTL_DISK_BASE,0x50,METHOD_BUFFERED,FILE_ANY_ACCESS)
const ioctlDiskUpdateProperties = 0x70140

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

	var dummy uint32
	var handles []syscall.Handle
	for _, v := range getVolumesForDisk(disk) {
		fd, err := syscall.CreateFile(syscall.StringToUTF16Ptr(v), syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, 0, 0)
		if err != nil {
			return fmt.Errorf("failed to open %s: %v", v, err)
		}
		// https://msdn.microsoft.com/en-us/library/windows/desktop/aa364575.aspx
		// "Note that without a successful lock operation, a dismounted volume may
		// be remounted by any process at any time"
		err = syscall.DeviceIoControl(fd, fsctlLockVolume, nil, 0, nil, 0, &dummy, nil)
		if err != nil {
			syscall.CloseHandle(fd)
			return fmt.Errorf("failed to lock %s: %v", v, err)
		}
		// https://msdn.microsoft.com/en-us/library/windows/desktop/aa364562.aspx
		//   "It is important to lock the volume first, otherwise unpredictable
		//   behavior may happen."
		err = syscall.DeviceIoControl(fd, fsctlDismountVolume, nil, 0, nil, 0, &dummy, nil)
		if err != nil {
			syscall.CloseHandle(fd)
			return fmt.Errorf("failed to unmount %s: %v", v, err)
		}
		handles = append(handles, fd)
	}
	defer func() {
		// Closing the handle implicitly removes the lock.
		for _, h := range handles {
			syscall.CloseHandle(h)
		}
	}()

	fd, err := syscall.Open(disk, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			syscall.CloseHandle(fd)
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
		nw, err := syscall.Write(fd, b[:n])
		if err != nil {
			return fmt.Errorf("failed to write %s: %v", disk, err)
		}
		if nw != n {
			return errors.New("buffer underflow")
		}
	}
	// Refresh partition table.
	// https://msdn.microsoft.com/en-us/library/windows/desktop/aa365192.aspx
	err = syscall.DeviceIoControl(fd, ioctlDiskUpdateProperties, nil, 0, nil, 0, &dummy, nil)
	if err != nil {
		return fmt.Errorf("failed to refresh partition table on %s: %v", disk, err)
	}
	closed = true
	return syscall.CloseHandle(fd)
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

func getVolumesForDisk(disk string) []string {
	return nil
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
