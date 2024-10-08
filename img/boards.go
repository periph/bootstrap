// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package img

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ulikunitz/xz"
)

// Manufacturer is a board brand manufacturer.
type Manufacturer string

const (
	// HardKernel can be bought at http://hardkernel.com
	HardKernel Manufacturer = "hardkernel"
	// Raspberry is Raspberry Pi foundation; https://www.raspberrypi.org/about/
	Raspberry Manufacturer = "raspberrypi"

	// NextThingCo was a company at https://getchip.com
	NextThingCo Manufacturer = "ntc"
)

func (m *Manufacturer) String() string {
	return string(*m)
}

var manufacturers = []Manufacturer{HardKernel, NextThingCo, Raspberry}

// Set implements flag.Value.
func (m *Manufacturer) Set(s string) error {
	for _, e := range manufacturers {
		if s == string(e) {
			*m = Manufacturer(e)
			return nil
		}
	}
	return errors.New("unsupported manufacturer")
}

// ManufacturerHelp generates the help for Manufacturer.
func ManufacturerHelp() string {
	var names []string
	for _, e := range manufacturers {
		names = append(names, string(e))
	}
	sort.Strings(names)
	return fmt.Sprintf("Board manufacturer: %s", strings.Join(names, ", "))
}

// boards return the boards that need a separate image.
func (m *Manufacturer) boards() []Board {
	switch *m {
	case HardKernel:
		return []Board{OdroidC1}
	case NextThingCo:
		return []Board{CHIP, CHIPPro, PocketCHIP}
	case Raspberry:
		// All boards use the same images on RaspiOS, so from our point of view,
		// they are all the same.
		// TODO(maruel): That's not true for Ubuntu, since they provide arm64
		// images that only works on RPi3 and later.
		return []Board{RaspberryPi}
	default:
		return nil
	}
}

// distros return the distros valid.
func (m *Manufacturer) distros() []Distro {
	switch *m {
	case HardKernel:
		return []Distro{Ubuntu}
	case NextThingCo:
		// debian-headless
		return []Distro{Debian}
	case Raspberry:
		// raspios-lite
		return []Distro{RaspiOS, RaspiOS64, Ubuntu}
	default:
		return nil
	}
}

//

// Board is a board from a brand manufacturer.
type Board string

const (
	// OdroidC1 is a board sold by HardKernel.
	OdroidC1 Board = "odroidc1"
	// RaspberryPi is a series of boards sold by Raspberry.
	RaspberryPi Board = "raspberrypi"

	// CHIP used to be sold by NextThingCo.
	CHIP Board = "chip"
	// CHIPPro used to be sold by NextThingCo.
	CHIPPro Board = "chippro"
	// PocketCHIP used to be sold by NextThingCo.
	PocketCHIP Board = "pocketchip"
)

var boards = []Board{OdroidC1, RaspberryPi, CHIP, CHIPPro, PocketCHIP}

func (b *Board) String() string {
	return string(*b)
}

// Set implements flag.Value.
func (b *Board) Set(s string) error {
	for _, e := range boards {
		if s == string(e) {
			*b = Board(e)
			return nil
		}
	}
	return errors.New("unsupported board")
}

// BoardHelp generates the help for Board.
func BoardHelp() string {
	var names []string
	for _, e := range boards {
		names = append(names, string(e))
	}
	sort.Strings(names)
	return fmt.Sprintf("Boards: %s", strings.Join(names, ", "))
}

// Distro is an OS distribution.
type Distro string

const (
	// Debian is https://www.debian.org/
	Debian Distro = "debian"
	// RaspiOS is Raspberry Pi OS Lite.
	// See https://www.raspberrypi.com/software/operating-systems/
	RaspiOS Distro = "raspios"
	// RaspiOS64 is Raspberry Pi OS 64 Lite.
	// See https://www.raspberrypi.com/software/operating-systems/
	RaspiOS64 Distro = "raspios64"
	// Ubuntu is https://ubuntu.com/
	Ubuntu Distro = "ubuntu"
)

var distros = []Distro{Debian, RaspiOS, RaspiOS64, Ubuntu}

func (d *Distro) String() string {
	return string(*d)
}

// Set implements flag.Value.
func (d *Distro) Set(s string) error {
	for _, e := range distros {
		if s == string(e) {
			*d = Distro(e)
			return nil
		}
	}
	return errors.New("unsupported distro")
}

// DistroHelp generates the help for Distro.
func DistroHelp() string {
	names := make([]string, len(distros))
	for i, d := range distros {
		names[i] = string(d)
	}
	return fmt.Sprintf("Distros: %s", strings.Join(names, ", "))
}

//

// Image is an image that can be used on a board by a manufacturer.
type Image struct {
	Manufacturer Manufacturer
	Board        Board
	Distro       Distro
}

func (i *Image) String() string {
	return fmt.Sprintf("%s:%s:%s", i.Manufacturer, i.Board, i.Distro)
}

// Check sets default values and confirm specified values.
func (i *Image) Check() error {
	if i.Manufacturer == "" {
		if i.Board == "" {
			return errors.New("specify at least one of manufacturer or board")
		}
		// Reverse lookup.
		switch i.Board {
		case CHIP, CHIPPro, PocketCHIP:
			i.Manufacturer = NextThingCo
		case OdroidC1:
			i.Manufacturer = HardKernel
		case RaspberryPi:
			i.Manufacturer = Raspberry
		default:
			return errors.New("unknown board")
		}
	} else {
		b := i.Manufacturer.boards()
		if len(b) == 0 {
			return errors.New("unknown manufacturer")
		}
		if i.Board == "" {
			i.Board = b[0]
		} else {
			found := false
			for _, j := range b {
				if i.Board == j {
					found = true
					break
				}
			}
			if !found {
				return errors.New("unknown board")
			}
		}
	}

	if i.Distro == "" {
		di := i.Manufacturer.distros()
		if len(di) == 0 {
			return errors.New("unknown manufacturer")
		}
		i.Distro = di[0]
	}
	return nil
}

// DefaultUser returns the default user account created by the image.
func (i *Image) DefaultUser() string {
	switch i.Manufacturer {
	case HardKernel:
		return "chip"
	case NextThingCo:
		return "odroid"
	case Raspberry:
		switch i.Distro {
		case RaspiOS, RaspiOS64:
			return "pi"
		case Ubuntu:
			return "ubuntu"
		}
	}
	return ""
}

// DefaultHostname returns the default hostname as set by the image.
func (i *Image) DefaultHostname() string {
	switch i.Manufacturer {
	case HardKernel:
		return "chip"
	case NextThingCo:
		return "odroid"
	case Raspberry:
		return "raspberrypi"
	default:
		return ""
	}
}

// Fetch fetches the distro image remotely.
//
// Returns the absolute path to the file downloaded.
func (i *Image) Fetch() (string, error) {
	switch i.Manufacturer {
	case HardKernel:
		return fetchHardKernel()
	case NextThingCo:
		return "", errors.New("implement me")
	case Raspberry:
		switch i.Distro {
		case RaspiOS:
			return fetchRPiRaspiOSLite(false)
		case RaspiOS64:
			return fetchRPiRaspiOSLite(true)
		case Ubuntu:
			return fetchRPiUbuntu()
		}
	}
	// - https://www.armbian.com/download/
	// - https://beagleboard.org/latest-images better to flash then run setup.sh
	//   manually.
	// - https://flash.getchip.com/ better to flash then run setup.sh manually.
	return "", fmt.Errorf("don't know how to fetch %s", i)
}

func fetchHardKernel() (string, error) {
	// http://odroid.com/dokuwiki/doku.php?id=en:odroid-c1
	// http://odroid.in/ubuntu_16.04lts/
	mirror := "https://odroid.in/ubuntu_16.04lts/"
	// http://east.us.odroid.in/ubuntu_16.04lts
	// http://de.eu.odroid.in/ubuntu_16.04lts
	// http://dn.odroid.com/S805/Ubuntu
	imgpath, err := filepath.Abs("ubuntu-16.04.2-minimal-odroid-c1-20170221.img")
	if err != nil {
		return "", err
	}
	if f, _ := os.Open(imgpath); f != nil /* #nosec G304 */ {
		fmt.Printf("- Reusing Ubuntu minimal image %s\n", imgpath)
		_ = f.Close()
		return imgpath, nil
	}
	imgurl := mirror + imgpath + ".xz"
	if err := fetchXZ(imgurl, imgpath); err != nil {
		return "", err
	}
	return imgpath, nil
}

func fetchRPiRaspiOSLite(is64bits bool) (string, error) {
	imgurl, imgname := raspiosGetLatestImageURL(is64bits)
	imgpath, err := filepath.Abs(imgname)
	if err != nil {
		return "", err
	}
	if f, _ := os.Open(imgpath); f != nil /* #nosec G304 */ {
		name := "RaspiOS"
		if is64bits {
			name += "64"
		}
		fmt.Printf("- Reusing %s Lite image %s\n", name, imgpath)
		_ = f.Close()
		return imgpath, nil
	}
	if err := fetchXZ(imgurl, imgname); err != nil {
		return "", err
	}
	return imgname, nil
}

func fetchRPiUbuntu() (string, error) {
	// https://ubuntu.com/download/raspberry-pi
	// For now, if the user requests ubuntu, assume they want the 64 bits version.
	// TODO(maruel): Do not hardcode the version.
	ver := "20.04"
	imgname := "ubuntu-" + ver + "-preinstalled-server-arm64+raspi.img"
	imgpath, err := filepath.Abs(imgname)
	if err != nil {
		return "", err
	}
	if f, _ := os.Open(imgpath); f != nil /* #nosec G304 */ {
		fmt.Printf("- Reusing Ubuntu %s image %s\n", ver, imgpath)
		_ = f.Close()
		return imgpath, nil
	}
	imgurl := "http://cdimage.ubuntu.com/releases/" + ver + "/release/" + imgname + ".xz"
	if err := fetchXZ(imgurl, imgpath); err != nil {
		return "", err
	}
	return imgpath, nil
}

//

// raspiosGetLatestImageURL reads the image listing to find the latest one.
//
// Getting the torrent would be nicer to the host.
func raspiosGetLatestImageURL(is64bits bool) (string, string) {
	// The final URL looks like:
	// https://downloads.raspberrypi.org/raspios_lite_armhf/images/raspios_lite_armhf-2022-09-26/2022-09-22-raspios-bullseye-armhf-lite.img.xz
	arch := "armhf"
	if is64bits {
		arch = "arm64"
	}
	baseImgURL := "https://downloads.raspberrypi.org/raspios_lite_" + arch + "/images/"
	dirFmt := "raspios_lite_" + arch + "-%s/"
	re1 := regexp.MustCompile(`raspios_lite_` + arch + `-(20\d\d-\d\d-\d\d)/`)
	re2 := regexp.MustCompile(`(20\d\d-\d\d-\d\d-raspios-[[:alpha:]]+-` + arch + `-lite\.img\.xz)`)
	var matches [][][]byte
	var match [][]byte

	// Use a recent (as of now) default date, it's not a big deal if the image is
	// a bit stale, it'll just take more time to "apt upgrade".
	date := "2022-09-26"
	// TODO(maruel): Figure out the distro automatically.
	distro := "bullseye"
	// It's a bit annoying as the image date and the directory date do not match.
	xzFile := "2022-09-22" + "-raspios-" + distro + "-" + arch + "-lite.img.xz"
	imgFile := "2022-09-22" + "-raspios-" + distro + "-" + arch + "-lite.img"

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
	log.Printf("Found date %s", date)

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
	xzFile = string(match[1])
	log.Printf("Found xzfile %s", xzFile)
	imgFile = xzFile[:len(xzFile)-3]

end:
	url := baseImgURL + fmt.Sprintf(dirFmt, date) + xzFile
	name := "RaspiOS"
	if is64bits {
		name += "64"
	}
	log.Printf("%s date: %s", name, date)
	log.Printf("%s distro: %s", name, distro)
	log.Printf("%s URL: %s", name, url)
	log.Printf("%s file: %s", name, imgFile)
	return url, imgFile
}

func fetchURL(url string) ([]byte, error) {
	r, err := http.DefaultClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %q: %w", url, err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch %q: status %d", url, r.StatusCode)
	}
	reply, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", url, err)
	}
	return reply, nil
}

func fetchXZ(imgurl, imgpath string) error {
	fmt.Printf("- Fetching %s\n", imgurl)
	resp, err := http.DefaultClient.Get(imgurl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	r, err := xz.NewReader(resp.Body)
	if err != nil {
		return err
	}
	/* #nosec G304 */
	f, err := os.Create(imgpath)
	if err != nil {
		return err
	}
	// Decompress as the file is being downloaded.
	if _, err = io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
