// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// Package img implements OS image related functionality for micro computers.
//
// It includes fetching images and flashing them on an SDCard.
//
// It includes gathering environmental information, like the current country
// and location on the host to enable configuring the board with the same
// settings.
//
package img // import "periph.io/x/bootstrap/img"

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/DHowett/go-plist"
)

// GetTimeLocation returns the time location, e.g. America/Toronto.
func GetTimeLocation() string {
	// OSX and Ubuntu
	if d, _ := os.Readlink("/etc/localtime"); len(d) != 0 {
		const p = "/usr/share/zoneinfo/"
		if strings.HasPrefix(d, p) {
			return d[len(p):]
		}
	}
	// systemd
	if d, _ := exec.Command("timedatectl").Output(); len(d) != 0 {
		re := regexp.MustCompile(`(?m)Time zone\: ([^\s]+)`)
		if match := re.FindSubmatch(d); len(match) != 0 {
			return string(match[1])
		}
	}
	// TODO(maruel): Windows.
	return "Etc/UTC"
}

// GetCountry returns the automatically detected country.
//
// WARNING: This causes an outgoing HTTP request.
func GetCountry() string {
	// TODO(maruel): Ask the OS first if possible.
	b, err := fetchURL("https://ipinfo.io/country")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// GetSetupSH returns the content of setup.sh.
//
// Returns nil in case of catastrophic error.
func GetSetupSH() []byte {
	var p []string
	if v, err := os.Getwd(); err == nil {
		p = append(p, v)
	}
	if gp := os.Getenv("GOPATH"); len(gp) != 0 {
		for _, v := range strings.Split(gp, string(os.PathListSeparator)) {
			p = append(p, filepath.Join(v, "go", "src", "periph.io", "x", "bootstrap"))
		}
	} else {
		p = append(p, filepath.Join(getHome(), "go", "src", "periph.io", "x", "bootstrap"))
	}
	for _, v := range p {
		b, err := ioutil.ReadFile(filepath.Join(v, "setup.sh"))
		if err == nil && len(b) != 0 {
			return b
		}
	}
	b, _ := fetchURL("https://raw.githubusercontent.com/periph/bootstrap/master/setup.sh")
	return b
}

// FindPublicKey returns the absolute path to a public key for the user, if any.
func FindPublicKey() string {
	home := getHome()
	for _, i := range []string{"authorized_keys", "id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
		p := filepath.Join(home, ".ssh", i)
		if f, _ := os.Open(p); f != nil {
			f.Close()
			return p
		}
	}
	return ""
}

// ListSDCards returns the SD cards found.
//
// Returns nil in case of error.
func ListSDCards() []string {
	switch runtime.GOOS {
	case "linux":
		return listSDCardsLinux()
	case "darwin":
		return listSDCardsOSX()
	default:
		return nil
	}
}

// Flash flashes imgPath to disk.
//
// Before flashing, it unmounts any partition mounted on disk.
func Flash(imgPath, disk string) error {
	if err := Umount(disk); err != nil {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		if err := ddFlash(imgPath, toRawDiskOSX(disk)); err != nil {
			return err
		}
		time.Sleep(time.Second)
		// Assumes this image has at least one partition.
		p := disk + "s1"
		for {
			if _, err := os.Stat(p); err == nil {
				break
			}
			fmt.Printf(" (still waiting for partition %s to show up)\n", p)
			time.Sleep(time.Second)
		}
		return nil
	case "linux":
		if err := ddFlash(imgPath, disk); err != nil {
			return err
		}
		// Wait a bit to try to workaround "Error looking up object for device" when
		// immediately using "/usr/bin/udisksctl mount" after this script.
		time.Sleep(time.Second)
		// Needs suffix 'p' for /dev/mmcblkN but not for /dev/sdX
		p := disk
		if strings.Contains(p, "mmcblk") {
			p += "p"
		}
		// Assumes this image has at least one partition.
		p += "1"
		for {
			if _, err := os.Stat(p); err == nil {
				break
			}
			fmt.Printf(" (still waiting for partition %s to show up)\n", p)
			time.Sleep(time.Second)
		}
		return nil
	default:
		return errors.New("Flash() is not implemented on this OS")
	}
}

// Mount mounts a partition number n on disk p and returns the mount path.
func Mount(disk string, n int) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		// diskutil doesn't report which volume was mounted, so look at the ones
		// before and the ones after and hope for the best.
		before, err := getMountedVolumesOSX()
		if err != nil {
			return "", err
		}
		mnt := fmt.Sprintf("%ss%d", disk, n)
		log.Printf("- Mounting %s", mnt)
		if _, err = capture("", "diskutil", "mountDisk", mnt); err != nil {
			return "", err
		}
		after, err := getMountedVolumesOSX()
		if err != nil {
			return "", err
		}
		if len(before)+1 != len(after) {
			return "", errors.New("unexpected number of mounted drives")
		}
		found := ""
		for i, a := range after {
			if i == len(before) || a != before[i] {
				found = "/Volumes/" + a
				break
			}
		}
		log.Printf("  Mounted as %s", found)
		return found, nil
	case "linux":
		// Needs 'p' for /dev/mmcblkN but not for /dev/sdX
		if strings.Contains(disk, "mmcblk") {
			disk += "p"
		}
		mnt := fmt.Sprintf("%s%d", disk, n)
		log.Printf("- Mounting %s", mnt)
		// TODO(maruel): This assumes Ubuntu.
		txt, _ := capture("", "/usr/bin/udisksctl", "mount", "-b", mnt)
		if match := reMountLinux1.FindStringSubmatch(txt); len(match) != 0 {
			log.Printf("  Mounted as %s", match[1])
			return match[1], nil
		}
		if match := reMountLinux2.FindStringSubmatch(txt); len(match) != 0 {
			log.Printf("  Mounted as %s", match[1])
			return match[1], nil
		}
		return "", fmt.Errorf("failed to mount %q: %q", mnt, txt)
	default:
		return "", errors.New("Mount() is not implemented on this OS")
	}
}

// Umount unmounts all the partitions on disk 'disk'.
func Umount(disk string) error {
	switch runtime.GOOS {
	case "darwin":
		log.Printf("- Unmounting %s", disk)
		_, _ = capture("", "diskutil", "unmountDisk", disk)
		return nil
	case "linux":
		matches, err := filepath.Glob(disk + "*")
		if err != nil {
			return err
		}
		sort.Strings(matches)
		for _, m := range matches {
			if m != disk {
				// TODO(maruel): This assumes Ubuntu.
				log.Printf("- Unmounting %s", m)
				if _, err1 := capture("", "/usr/bin/udisksctl", "unmount", "-f", "-b", m); err == nil {
					err = err1
				}
			}
		}
		return nil
	default:
		return errors.New("Umount() is not implemented on this OS")
	}
}

//

// run runs a command.
func run(name string, arg ...string) error {
	log.Printf("run(%s %s)", name, strings.Join(arg, " "))
	cmd := exec.Command(name, arg...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// capture runs a command and return the stdout and stderr merged.
func capture(in, name string, arg ...string) (string, error) {
	//log.Printf("capture(%s %s)", name, strings.Join(arg, " "))
	cmd := exec.Command(name, arg...)
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func getHome() string {
	if usr, err := user.Current(); err == nil && len(usr.HomeDir) != 0 {
		return usr.HomeDir
	}
	return os.Getenv("HOME")
}

func ddFlash(imgPath, dst string) error {
	fmt.Printf("- Flashing (takes 2 minutes)\n")
	// OSX uses 'M' but Ubuntu uses 'm' but using numbers works everywhere.
	if err := run("sudo", "dd", fmt.Sprintf("bs=%d", 4*1024*1024), "if="+imgPath, "of="+dst); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		// Tells the OS to wake up with the fact that the partitions changed. It's
		// fine even if the cache is not written to the disk yet, as the cached
		// data is in the OS cache. :)
		if err := run("sudo", "partprobe"); err != nil {
			return err
		}
	}
	// This step may take a while for writeback cache.
	fmt.Printf("- Flushing I/O cache\n")
	if err := run("sudo", "sync"); err != nil {
		return err
	}
	return nil
}

// Linux

var (
	// "Mounted /dev/sdh2 at /media/<user>/<GUID>."
	reMountLinux1 = regexp.MustCompile(`Mounted (?:[^ ]+) at ([^\\]+)\..*`)
	// "Error mounting /dev/sdh2: GDBus.Error:org.freedesktop.UDisks2.Error.AlreadyMounted: Device /dev/sdh2"
	// "is already mounted at `/media/<user>/<GUID>'.
	reMountLinux2 = regexp.MustCompile(`is already mounted at ` + "`" + `([^\']+)\'`)
)

type lsblk struct {
	BlockDevices []struct {
		Name       string
		MajMin     string `json:"maj:min"`
		RM         string
		Size       string
		RO         string
		Type       string
		MountPoint string
	}
}

func listSDCardsLinux() []string {
	b, err := capture("", "lsblk", "--json")
	if err != nil {
		return nil
	}
	v := lsblk{}
	err = json.Unmarshal([]byte(b), &v)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, dev := range v.BlockDevices {
		if dev.RM == "1" && dev.RO == "0" && dev.Type == "disk" {
			out = append(out, "/dev/"+dev.Name)
		}
	}
	return out
}

// OSX

type diskutilList struct {
	AllDisks              []string
	AllDisksAndPartitions []struct {
		Content          string
		DeviceIdentifier string
		Partitions       []map[string]interface{}
		MountPoint       string
		Size             int64
		VolumeName       string
	}
	VolumesFromDisks []string
	WholeDisks       []string
}

type diskutilInfo struct {
	Bootable                                    bool
	BusProtocol                                 string
	CanBeMadeBootable                           bool
	CanBeMadeBootableRequiresDestroy            bool
	content                                     string
	DeviceBlockSize                             int64
	DeviceIdentifier                            string
	DeviceNode                                  string
	DeviceTreePath                              string
	Ejectable                                   bool
	EjectableMediaAutomaticUnderSoftwareControl bool
	EjectableOnly                               bool
	FreeSpace                                   int64
	GlobalPermissionsEnabled                    bool
	IOKitSize                                   int64
	IORegistryEntryName                         string
	Internal                                    bool
	LowLevelFormatSupported                     bool
	MediaName                                   string
	MediaType                                   string
	MountPoint                                  string
	OS9DriversInstalled                         bool
	ParentWholeDisk                             string
	RAIDMaster                                  bool
	RAIDSlice                                   bool
	Removable                                   bool
	RemovableMedia                              bool
	RemovableMediaOrExternalDevice              bool
	SMARTStatus                                 string
	Size                                        int64
	SupportsGlobalPermissionsDisable            bool
	SystemImage                                 bool
	TotalSize                                   int64
	VirtualOrPhysical                           string
	VolumeName                                  string
	VolumeSize                                  int64
	WholeDisk                                   bool
	Writable                                    bool
	WritableMedia                               bool
	WritableVolume                              bool
}

func listSDCardsOSX() []string {
	b, err := capture("", "diskutil", "list", "-plist")
	if err != nil {
		return nil
	}
	disks := diskutilList{}
	_, err = plist.Unmarshal([]byte(b), &disks)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range disks.WholeDisks {
		b, err = capture("", "diskutil", "info", "-plist", d)
		if err != nil {
			continue
		}
		info := diskutilInfo{}
		_, err = plist.Unmarshal([]byte(b), &info)
		if err != nil {
			continue
		}
		if info.RemovableMedia && info.Writable {
			out = append(out, info.DeviceNode)
		}
	}
	return out
}

// toRawDiskOSX replaces a path to a buffered disk to the raw equivalent device
// node.
//
// rdisk is several times faster than disk.
func toRawDiskOSX(p string) string {
	const prefix = "/dev/disk"
	if strings.HasPrefix(p, prefix) {
		return "/dev/rdisk" + p[len(prefix):]
	}
	return p
}

func getMountedVolumesOSX() ([]string, error) {
	f, err := os.Open("/Volumes")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	all, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}
	var actual []string
	for _, f := range all {
		if f.Mode()&os.ModeSymlink == 0 {
			actual = append(actual, f.Name())
		}
	}
	sort.Strings(actual)
	return actual, nil
}
