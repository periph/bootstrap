// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// Package img implements image related functionality.
package img // import "periph.io/x/bootstrap/img"

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func ddFlash(imgPath, dst string) error {
	fmt.Printf("- Flashing (takes 2 minutes)\n")
	// OSX uses 'M' but Ubuntu uses 'm'.
	if err := Run("sudo", "dd", fmt.Sprintf("bs=%d", 4*1024*1024), "if="+imgPath, "of="+dst); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		// Tells the OS to wake up with the fact that the partitions changed. It's
		// fine even if the cache is not written to the disk yet, as the cached
		// data is in the OS cache. :)
		if err := Run("sudo", "partprobe"); err != nil {
			return err
		}
	}
	// This step may take a while for writeback cache.
	fmt.Printf("- Flushing I/O cache\n")
	if err := Run("sudo", "sync"); err != nil {
		return err
	}
	return nil
}

// Flash flashes imgPath to dst.
func Flash(imgPath, dst string) error {
	switch runtime.GOOS {
	case "darwin":
		_, _ = Capture("", "diskutil", "unmountDisk", dst)
		return ddFlash(imgPath, dst)
	case "linux":
		return ddFlash(imgPath, dst)
	default:
		return errors.New("Flash() is not implemented on this OS")
	}
}

// CopyFile copies src from dst.
func CopyFile(dst, src string, mode os.FileMode) error {
	fs, err := os.Open(src)
	if err != nil {
		return err
	}
	defer fs.Close()
	fd, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fd, fs); err != nil {
		fd.Close()
		return err
	}
	return fd.Close()
}

// Run runs a command.
func Run(name string, arg ...string) error {
	log.Printf("Run(%s %s)", name, strings.Join(arg, " "))
	cmd := exec.Command(name, arg...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Capture runs a command and return the stdout and stderr merged.
func Capture(in, name string, arg ...string) (string, error) {
	//log.Printf("Capture(%s %s)", name, strings.Join(arg, " "))
	cmd := exec.Command(name, arg...)
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

var (
	// "Mounted /dev/sdh2 at /media/<user>/<GUID>."
	reMountLinux1 = regexp.MustCompile(`Mounted (?:[^ ]+) at ([^\\]+)\..*`)
	// "Error mounting /dev/sdh2: GDBus.Error:org.freedesktop.UDisks2.Error.AlreadyMounted: Device /dev/sdh2"
	// "is already mounted at `/media/<user>/<GUID>'.
	reMountLinux2 = regexp.MustCompile(`is already mounted at ` + "`" + `([^\']+)\'`)
)

// Mount mounts a partition and returns the mount path.
func Mount(p string) (string, error) {
	switch runtime.GOOS {
	case "linux":
		// TODO(maruel): This assumes Ubuntu.
		log.Printf("- Mounting %s", p)
		txt, _ := Capture("", "/usr/bin/udisksctl", "mount", "-b", p)
		if match := reMountLinux1.FindStringSubmatch(txt); len(match) != 0 {
			return match[1], nil
		}
		if match := reMountLinux2.FindStringSubmatch(txt); len(match) != 0 {
			return match[1], nil
		}
		return "", fmt.Errorf("failed to mount %q: %q", p, txt)
	default:
		return "", errors.New("Mount() is not implemented on this OS")
	}
}

// Umount unmounts all the partitions on disk 'p'.
func Umount(p string) error {
	switch runtime.GOOS {
	case "linux":
		// TODO(maruel): This assumes Ubuntu.
		matches, err := filepath.Glob(p + "*")
		if err != nil {
			return err
		}
		sort.Strings(matches)
		for _, m := range matches {
			if m != p {
				log.Printf("- Unmounting %s", m)
				if _, err1 := Capture("", "/usr/bin/udisksctl", "unmount", "-f", "-b", m); err == nil {
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

func getHome() string {
	if usr, err := user.Current(); err == nil && len(usr.HomeDir) != 0 {
		return usr.HomeDir
	}
	return os.Getenv("HOME")
}

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
	b, err := Capture("", "lsblk", "--json")
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
	b, err := Capture("", "diskutil", "list", "-plist")
	if err != nil {
		return nil
	}
	disks := diskutilList{}
	_, err = plist.Unmarshal([]byte(b), &disks)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, d := range disks.WholeDisks {
		b, err = Capture("", "diskutil", "info", "-plist", d)
		if err != nil {
			continue
		}
		info := diskutilInfo{}
		_, err = plist.Unmarshal([]byte(b), &info)
		if err != nil {
			continue
		}
		if info.RemovableMedia && info.Writable {
			// rdisk is faster than disk so construct it manually instead of using
			// info.DeviceNode.
			out = append(out, "/dev/r"+d)
		}
	}
	return out
}
