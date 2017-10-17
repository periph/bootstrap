// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// Package img implements image related functionality.
package img // import "periph.io/x/bootstrap/img"

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/ulikunitz/xz"
)

// RaspberryPi3UART is the part to append to /boot/config.txt to enable UART on
// RaspberryPi 3.
const RaspberryPi3UART = `

# Enable console on UART on RPi3
# https://www.raspberrypi.org/forums/viewtopic.php?f=28&t=141195
[pi3]
enable_uart=1
[all]
`

// RcLocalContent is the content to append to /etc/rc.local to shell out
// /boot/firstboot.sh.
const RcLocalContent = `

# The following was added by cmd/flash from
# https://github.com/periph/bootstrap

LOG_FILE=/var/log/firstboot.log
if [ ! -f $LOG_FILE ]; then
  %s/firstboot.sh%s 2>&1 | tee $LOG_FILE
fi
exit 0
`

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
	resp, err := http.DefaultClient.Get("https://ipinfo.io/country")
	if err != nil {
		return ""
	}
	b, err := ioutil.ReadAll(resp.Body)
	err2 := resp.Body.Close()
	if err != nil || err2 != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// GetPath returns path to $GOPATH/src/periph.io/x/bootstrap.
func GetPath() string {
	gp := os.Getenv("GOPATH")
	if len(gp) == 0 {
		gp = filepath.Join(getHome(), "go")
	} else {
		gp = strings.SplitN(gp, string(os.PathListSeparator), 2)[0]
	}
	return filepath.Join(gp, "src", "periph.io", "x", "bootstrap")
}

// FindPublicKey returns a public key for the user if any.
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

// Flash flashes imgPath to dst.
func Flash(imgPath, dst string) error {
	switch runtime.GOOS {
	case "linux":
		fmt.Printf("- Flashing (takes 2 minutes)\n")
		if err := Run("dd", "bs=4M", "if="+imgPath, "of="+dst); err != nil {
			return err
		}
		fmt.Printf("- Flushing I/O cache\n")
		return Run("sync")
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
	log.Printf("Capture(%s %s)", name, strings.Join(arg, " "))
	cmd := exec.Command(name, arg...)
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Manufacturer is a board brand manufacturer.
type Manufacturer string

const (
	// HardKernel can be bought at http://hardkernel.com
	HardKernel Manufacturer = "hardkernel"
	// NextThingCo can be bought at https://getchip.com
	NextThingCo Manufacturer = "ntc"
	// RaspberryPi is Raspberry Pi foundation; https://www.raspberrypi.org/about/
	RaspberryPi Manufacturer = "raspberrypi"
)

func (m *Manufacturer) String() string {
	return string(*m)
}

// Set implements flag.Value.
func (m *Manufacturer) Set(s string) error {
	switch Manufacturer(s) {
	case HardKernel, NextThingCo, RaspberryPi:
		*m = Manufacturer(s)
		return nil
	default:
		return errors.New("unsupported manufacturer")
	}
}

// ManufacturerHelp generates the help for Manufacturer.
func ManufacturerHelp() string {
	var names []string
	for _, k := range []Manufacturer{HardKernel, NextThingCo, RaspberryPi} {
		names = append(names, string(k))
	}
	sort.Strings(names)
	return fmt.Sprintf("Board manufacturer: %s", strings.Join(names, ", "))
}

// boards return the boards that need a separate image.
func (m *Manufacturer) boards() []Board {
	switch *m {
	case HardKernel:
		return []Board{"odroidc1"}
	case NextThingCo:
		return []Board{"chip", "chippro", "pocketchip"}
	case RaspberryPi:
		// All boards use the same images, so from our point of view, they are all
		// the same.
		return []Board{"raspberrypi"}
	default:
		return nil
	}
}

// distros return the distros valid.
func (m *Manufacturer) distros() []string {
	switch *m {
	case HardKernel:
		return []string{"ubuntu"}
	case NextThingCo:
		return []string{"debian-headless"}
	case RaspberryPi:
		return []string{"raspbian-lite"}
	default:
		return nil
	}
}

//

// Board is a board from a brand manufacturer.
type Board string

func (b *Board) String() string {
	return string(*b)
}

// Set implements flag.Value.
func (b *Board) Set(s string) error {
	bb := Board(s)
	for _, board := range boards {
		if bb == board {
			*b = bb
			return nil
		}
	}
	return errors.New("unsupported board")
}

// BoardHelp generates the help for Board.
func BoardHelp() string {
	names := make([]string, len(boards))
	for i, b := range boards {
		names[i] = string(b)
	}
	return fmt.Sprintf("Boards: %s", strings.Join(names, ", "))
}

var boards []Board

func init() {
	for _, k := range []Manufacturer{HardKernel, NextThingCo, RaspberryPi} {
		boards = append(boards, k.boards()...)
	}
}

//

// Distro is an image that can be used on a board by a manufacturer.
type Distro struct {
	Manufacturer Manufacturer
	Board        Board
	Distro       string
}

func (d *Distro) String() string {
	return fmt.Sprintf("%s:%s:%s", d.Manufacturer, d.Board, d.Distro)
}

// Check sets default values and confirm specified values.
func (d *Distro) Check() error {
	if d.Manufacturer == "" {
		if d.Board == "" {
			return errors.New("specify at least one of manufacturer or board")
		}
		// Reverse lookup.
		switch d.Board {
		case "chip", "chippro", "pocketchip":
			d.Manufacturer = NextThingCo
		case "odroidc1":
			d.Manufacturer = HardKernel
		case "raspberrypi":
			d.Manufacturer = RaspberryPi
		default:
			return errors.New("unknown board")
		}
	} else {
		b := d.Manufacturer.boards()
		if len(b) == 0 {
			return errors.New("unknown manufacturer")
		}
		if d.Board == "" {
			d.Board = b[0]
		} else {
			found := false
			for _, i := range b {
				if d.Board == i {
					found = true
					break
				}
			}
			if !found {
				return errors.New("unknown board")
			}
		}
	}

	di := d.Manufacturer.distros()
	if len(di) == 0 {
		return errors.New("unknown manufacturer")
	}
	d.Distro = di[0]
	return nil
}

// DefaultUser returns the default user account created by the image.
func (d *Distro) DefaultUser() string {
	switch d.Manufacturer {
	case HardKernel:
		return "chip"
	case NextThingCo:
		return "odroid"
	case RaspberryPi:
		return "pi"
	default:
		return ""
	}
}

// DefaultHostname returns the default hostname as set by the image.
func (d *Distro) DefaultHostname() string {
	switch d.Manufacturer {
	case HardKernel:
		return "chip"
	case NextThingCo:
		return "odroid"
	case RaspberryPi:
		return "raspberrypi"
	default:
		return ""
	}
}

// Fetch fetches the distro image remotely.
func (d *Distro) Fetch() (string, error) {
	switch d.Manufacturer {
	case HardKernel:
		return d.fetchHardKernel()
	case NextThingCo:
		return "", errors.New("implement me")
	case RaspberryPi:
		return d.fetchRaspberryPi()
	default:
		// - https://www.armbian.com/download/
		// - https://beagleboard.org/latest-images better to flash then run setup.sh
		//   manually.
		// - https://flash.getchip.com/ better to flash then run setup.sh manually.
		return "", fmt.Errorf("don't know how to fetch %s", d)
	}
}

func (d *Distro) fetchHardKernel() (string, error) {
	// http://odroid.com/dokuwiki/doku.php?id=en:odroid-c1
	// http://odroid.in/ubuntu_16.04lts/
	mirror := "https://odroid.in/ubuntu_16.04lts/"
	// http://east.us.odroid.in/ubuntu_16.04lts
	// http://de.eu.odroid.in/ubuntu_16.04lts
	// http://dn.odroid.com/S805/Ubuntu
	imgname := "ubuntu-16.04.2-minimal-odroid-c1-20170221.img"
	if f, _ := os.Open(imgname); f != nil {
		fmt.Printf("- Reusing Ubuntu minimal image %s\n", imgname)
		f.Close()
		return imgname, nil
	}
	fmt.Printf("- Fetching %s\n", imgname)
	resp, err := http.DefaultClient.Get(mirror + imgname + ".xz")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	r, err := xz.NewReader(resp.Body)
	if err != nil {
		return "", err
	}
	f, err := os.Create(imgname)
	if err != nil {
		return "", err
	}
	// Decompress as the file is being downloaded.
	if _, err = io.Copy(f, r); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return imgname, nil
}

func (d *Distro) fetchRaspberryPi() (string, error) {
	imgurl, imgname := raspbianGetLatestImageURL()
	if f, _ := os.Open(imgname); f != nil {
		fmt.Printf("- Reusing Raspbian Jessie Lite image %s\n", imgname)
		f.Close()
		return imgname, nil
	}
	fmt.Printf("- Fetching %s\n", imgname)
	// Read the whole file in memory. This is less than 300Mb. Save to disk if
	// it is too much for your system.
	// TODO(maruel): Progress bar?
	z, err := fetchURL(imgurl)
	if err != nil {
		return "", err
	}
	// Because zip header is at the end of the file, extraction can only begin
	// once the file is fully downloaded.
	fmt.Printf("- Extracting zip\n")
	r, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		return "", err
	}
	for _, fi := range r.File {
		if filepath.Base(fi.Name) == imgname {
			a, err := fi.Open()
			if err != nil {
				return "", err
			}
			f, err := os.Create(imgname)
			if err != nil {
				return "", err
			}
			if _, err = io.Copy(f, a); err != nil {
				f.Close()
				return "", err
			}
			if err := f.Close(); err != nil {
				return "", err
			}
			return imgname, nil
		}
	}
	return "", errors.New("failed to find image in zip")
}

//

// raspbianGetLatestImageURL reads the image listing to find the latest one.
//
// Getting the torrent would be nicer to the host.
func raspbianGetLatestImageURL() (string, string) {
	// This is where https://downloads.raspberrypi.org/raspbian_lite_latest
	// redirects to.
	const baseImgURL = "https://downloads.raspberrypi.org/raspbian_lite/images/"
	const dirFmt = "raspbian_lite-%s/"
	re1 := regexp.MustCompile(`raspbian_lite-(20\d\d-\d\d-\d\d)/`)
	re2 := regexp.MustCompile(`(20\d\d-\d\d-\d\d-raspbian-[[:alpha:]]+-lite\.zip)`)
	var matches [][][]byte
	var match [][]byte

	// Use a recent (as of now) default date, it's not a big deal if the image is
	// a bit stale, it'll just take more time to "apt upgrade".
	date := "2017-08-16"
	distro := "stretch"
	zipFile := date + "-raspbian-" + distro + "-lite.zip"
	imgFile := date + "-raspbian-" + distro + "-lite.img"

	r, err := fetchURL(baseImgURL)
	if err != nil {
		log.Printf("failed to fetch: %v", err)
		goto end
	}

	// This will be good until 2099.
	matches = re1.FindAllSubmatch(r, -1)
	if len(matches) == 0 {
		log.Printf("failed to match: %q", r)
		goto end
	}

	// It's already in sorted order.
	date = string(matches[len(matches)-1][1])

	// Find the distro name.
	r, err = fetchURL(baseImgURL + fmt.Sprintf(dirFmt, date))
	if err != nil {
		log.Printf("failed to fetch: %v", err)
		goto end
	}
	match = re2.FindSubmatch(r)
	if len(match) == 0 {
		log.Printf("failed to match: %q", r)
		goto end
	}
	zipFile = string(match[1])
	imgFile = zipFile[:len(zipFile)-3] + "img"

end:
	url := baseImgURL + fmt.Sprintf(dirFmt, date) + zipFile
	log.Printf("Raspbian date: %s", date)
	log.Printf("Raspbian distro: %s", distro)
	log.Printf("Raspbian URL: %s", url)
	log.Printf("Raspbian file: %s", imgFile)
	return url, imgFile
}

func fetchURL(url string) ([]byte, error) {
	r, err := http.DefaultClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch %q: %v", url, err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("Failed to fetch %q: status %d", url, r.StatusCode)
	}
	reply, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("Failed to read %q: %v", url, err)
	}
	return reply, nil
}

func getHome() string {
	if usr, err := user.Current(); err == nil && len(usr.HomeDir) != 0 {
		return usr.HomeDir
	}
	return os.Getenv("HOME")
}
