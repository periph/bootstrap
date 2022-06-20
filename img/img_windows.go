// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package img

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FSCTL_LOCK_VOLUME = CTL_CODE(FILE_DEVICE_FILE_SYSTEM,6,METHOD_BUFFERED,FILE_ANY_ACCESS)
const fsctlLockVolume = 0x90018

// FSCTL_DISMOUNT_VOLUME = CTL_CODE(FILE_DEVICE_FILE_SYSTEM,8,METHOD_BUFFERED,FILE_ANY_ACCESS)
const fsctlDismountVolume = 0x90020

// IOCTL_DISK_UPDATE_PROPERTIES = CTL_CODE(IOCTL_DISK_BASE,0x50,METHOD_BUFFERED,FILE_ANY_ACCESS)
const ioctlDiskUpdateProperties = 0x70140

// https://msdn.microsoft.com/en-us/library/windows/desktop/bb968801.aspx
// IOCTL_STORAGE_GET_DEVICE_NUMBER = CTL_CODE(IOCTL_STORAGE_BASE,0x0420,METHOD_BUFFERED,FILE_ANY_ACCESS)
const ioctlStorageGetDeviceNumber = 0x2d1080

// https://msdn.microsoft.com/en-us/library/windows/desktop/bb968801.aspx
type storageDeviceNumber struct {
	deviceType      uint32 // An enum.
	deviceNumber    uint32
	partitionNumber uint32
}

// flashWindows flashes the content of imgPath to physical disk 'disk'.
//
// Requires the process to be running as an admin account with an high level
// token.
//
// 'disk' is expected to be of format "\\\\.\\physicaldriveN"
func flashWindows(imgPath, disk string) error {
	// TODO(maruel): It'd be worth opening with FILE_FLAG_SEQUENTIAL_SCAN but Go
	// stdlib doesn't allow this.
	/* #nosec G304 */
	fi, err := os.Open(imgPath)
	if err != nil {
		return err
	}
	/* #nosec G307 */
	defer fi.Close()
	i, err := fi.Stat()
	if err != nil {
		return err
	}
	s := float64(i.Size())

	var dummy uint32
	var handles []syscall.Handle
	for _, v := range getVolumesForDisk(disk, 0) {
		var r *uint16
		if r, err = syscall.UTF16PtrFromString(v); err != nil {
			return err
		}
		var fd syscall.Handle
		if fd, err = syscall.CreateFile(r, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, 0, 0); err != nil {
			return fmt.Errorf("failed to open %s: %w", v, err)
		}
		// https://msdn.microsoft.com/en-us/library/windows/desktop/aa364575.aspx
		// "Note that without a successful lock operation, a dismounted volume may
		// be remounted by any process at any time"
		if err = syscall.DeviceIoControl(fd, fsctlLockVolume, nil, 0, nil, 0, &dummy, nil); err != nil {
			_ = syscall.CloseHandle(fd)
			return fmt.Errorf("failed to lock %s: %w", v, err)
		}
		// https://msdn.microsoft.com/en-us/library/windows/desktop/aa364562.aspx
		//   "It is important to lock the volume first, otherwise unpredictable
		//   behavior may happen."
		if err = syscall.DeviceIoControl(fd, fsctlDismountVolume, nil, 0, nil, 0, &dummy, nil); err != nil {
			_ = syscall.CloseHandle(fd)
			return fmt.Errorf("failed to unmount %s: %w", v, err)
		}
		// TODO(maruel): In practice, it'd be nicer to just delete the volumes?
		log.Println("locked volume", v)
		handles = append(handles, fd)
	}
	defer func() {
		// Closing the handle implicitly removes the lock.
		for _, h := range handles {
			_ = syscall.CloseHandle(h)
		}
	}()

	fd, err := syscall.Open(disk, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = syscall.CloseHandle(fd)
		}
	}()
	// Use manual buffer instead of io.Copy() to control buffer size. 64Kb should
	// be a multiple of all common sector sizes, generally 4Kb or 8Kb and it
	// should work better with the Windows' read-ahead mechanism.
	var b [64 * 1024]byte
	fmt.Printf("\n")
	for o := int64(0); ; {
		n := 0
		if n, err = fi.Read(b[:]); err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", imgPath, err)
		}
		nw := 0
		if nw, err = syscall.Write(fd, b[:n]); err != nil {
			// TODO(maruel): Find the drive letter(s) and call windows.DeleteVolumeMountPoint().
			return fmt.Errorf("failed to write %s. It likely means you need to unmount the drive letter: %w", disk, err)
		}
		if nw != n {
			return errors.New("buffer underflow")
		}
		o += int64(nw)
		fmt.Printf("\r%.1f%%", float64(o)*100./s)
	}
	fmt.Printf("\r100.0%%\n")
	// Refresh partition table.
	// https://msdn.microsoft.com/en-us/library/windows/desktop/aa365192.aspx
	err = syscall.DeviceIoControl(fd, ioctlDiskUpdateProperties, nil, 0, nil, 0, &dummy, nil)
	if err != nil {
		return fmt.Errorf("failed to refresh partition table on %s: %w", disk, err)
	}
	closed = true
	if err := syscall.CloseHandle(fd); err != nil {
		return err
	}
	// Closing the handle implicitly removes the lock. It is needed, otherwise
	// the new volumes won't appear.
	for _, h := range handles {
		_ = syscall.CloseHandle(h)
	}
	handles = nil

	// It will take a moment for the volumes to appear. Enforce a "sleep" by
	// calling mountWindows() for a few seconds until it succeeds.
	for start := time.Now(); time.Since(start) < 15*time.Second; {
		if _, err := mountWindows(disk, 1); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Still return nil, but mountWindows() will likely fail.
	return nil
}

// mountWindows find the volume path for the partition 'n' on disk 'disk'.
//
// The returned path is in form
// "\\\\?\\Volume{00000000-0000-0000-0000-000000000000}", not with a DOS drive
// letter. In practice it is simpler to work this way than try to find out the
// drive letter.
func mountWindows(disk string, n int) (string, error) {
	p := getVolumesForDisk(disk, n)
	if len(p) == 0 {
		return "", fmt.Errorf("partition #%d on disk %s not found", n, disk)
	}
	if len(p) > 1 {
		return "", fmt.Errorf("found multiple partitions #%d on disk %s: %v", n, disk, p)
	}
	return p[0], nil
}

func umountWindows(disk string) error {
	// This needs to be done *during* the flashing operation to keep the volumes
	// locked.
	return nil
}

func listSDCardsWindows() []string {
	var out []string
	// TODO(maruel): Do it directly instead of shelling out. A dumb loop over
	// "\\\\.\\physicaldriveN" from 0 to 50 would probably do it and would be
	// fast enough, at least faster than the current code.
	// https://msdn.microsoft.com/en-us/library/windows/desktop/aa394132.aspx
	for _, disk := range wmicList("diskdrive", "get", "medialoaded,mediatype,deviceid") {
		// Some USB devices report as fixed media, but we do not care since we
		// target only SDCards.
		if disk["MediaLoaded"] == "TRUE" && disk["MediaType"] == "Removable Media" {
			// String is in the format "\\\\.\\PHYSICALDRIVEn".
			out = append(out, strings.ToLower(disk["DeviceID"]))
		}
	}
	return out
}

//

// diskNum returns the disk number from its path.
//
// Disk numbers are 0 based.
func diskNum(disk string) int {
	disk = strings.ToLower(disk)
	const prefix = "\\\\.\\physicaldrive"
	if !strings.HasPrefix(disk, prefix) {
		return -1
	}
	i, err := strconv.Atoi(disk[len(prefix):])
	if err != nil {
		log.Println(disk, err)
		return -1
	}
	return i
}

// getVolumes returns all the volumes found on the system.
//
// The returned strings are in the format
// "\\\\?\\Volume{00000000-0000-0000-0000-000000000000}"
func getVolumes() []string {
	// TODO(maruel): Handle path overflow
	var v [256]uint16
	h, err := windows.FindFirstVolume(&v[0], uint32(len(v)))
	if err != nil {
		return nil
	}
	// Strip the trailing "\\" since it makes it unopenable.
	out := []string{strings.TrimSuffix(syscall.UTF16ToString(v[:]), "\\")}
	for {
		if err := windows.FindNextVolume(h, &v[0], uint32(len(v))); err != nil {
			break
		}
		out = append(out, strings.TrimSuffix(syscall.UTF16ToString(v[:]), "\\"))
	}
	_ = windows.FindVolumeClose(h)
	return out
}

// getVolumesForDisk enumerate all volumes, find the ones that are on the disk
// we care about.
//
// This is kinda backward but this is how Windows work.
//
// part is an optional partition volume to return, in this case the returned
// slice should have a length of 1. Partition numbers are 1 based. 0 means no
// filtering.
func getVolumesForDisk(disk string, part int) []string {
	num := diskNum(disk)
	if num == -1 {
		return nil
	}
	var out []string
	var bytesRead uint32
	l := uint32(reflect.TypeOf((*storageDeviceNumber)(nil)).Elem().Size())
	var b [32]byte
	for _, v := range getVolumes() {
		r, err := syscall.UTF16PtrFromString(v)
		if err != nil {
			log.Println(v, err)
			continue
		}
		fd, err := syscall.CreateFile(r, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, 0, 0)
		if err != nil {
			log.Println(v, err)
			continue
		}
		err = syscall.DeviceIoControl(fd, ioctlStorageGetDeviceNumber, nil, 0, &b[0], uint32(len(b)), &bytesRead, nil)
		_ = syscall.CloseHandle(fd)
		if err != nil {
			log.Println(v, len(b), err)
			continue
		}
		if bytesRead == l {
			/* #nosec G103 */
			s := (*storageDeviceNumber)(unsafe.Pointer(&b[0]))
			if int(s.deviceNumber) == num {
				if part == 0 || int(s.partitionNumber) == part {
					out = append(out, v)
				}
			}
		} else {
			log.Println("unexpected length", bytesRead)
		}
	}
	return out
}

// wmicList returns the parsed output from tool "wmic".
func wmicList(args ...string) []map[string]string {
	// TODO(maruel): It'd be nicer to use the Win32 APIs but shelling out will be
	// good enough for now. Shelling out permits to not have to do COM/WMI,
	// albeit https://github.com/go-ole/go-ole looks good and doesn't use cgo.
	//
	// In theory, /format:xml would produce an output meant to be mechanically
	// parsed, in practice the output is suboptimal.
	//
	// The 'new' way is
	// https://msdn.microsoft.com/en-us/library/windows/desktop/hh830612.aspx
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
