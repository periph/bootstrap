// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// efe automates flashing self-setup OS image to micro-computers.
//
// It fetches an OS image, makes a working copy, modifies the EXT4 root
// partition on it, flashes the modified image copy to an SDCard, mounts the
// SDCard and finally modifies the FAT32 boot partition.
//
// All this so it boots and self-setups itself automatically and sends an email
// when done.
package main // import "periph.io/x/bootstrap/cmd/efe"

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rekby/mbr"
	"golang.org/x/crypto/pbkdf2"
	"periph.io/x/bootstrap/img"
)

// oldRcLocal is the start of /etc/rc.local as found on Debian derived
// distributions before Debian 10 and Ubuntu 18.04.
//
// The comments are essentially the free space available to edit the file
// without having to understand EXT4. :)
//
// TODO(maruel): Find a new way for newer distributions.
const oldRcLocal = "#!/bin/sh -e\n#\n# rc.local\n#\n# This script is executed at the end of each multiuser runlevel.\n# Make sure that the script will \"exit 0\" on success or any other\n# value on error.\n#\n# In order to enable or disable this script just change the execution\n# bits.\n#\n# By default this script does nothing.\n"

// denseRcLocal is a 'dense' version of img.RcLocalContent.
const denseRcLocal = "#!/bin/sh -e\nL=/var/log/firstboot.log;if [ ! -f $L ];then /boot/firstboot.sh%s 2>&1|tee $L;fi\n#"

// raspberryPi3UART is the part to append to /boot/config.txt to enable UART on
// RaspberryPi 3.
const raspberryPi3UART = `

# Enable console on UART on RPi3
# https://www.raspberrypi.org/forums/viewtopic.php?f=28&t=141195
[pi3]
enable_uart=1
[all]
`

// raspberryPiWPASupplicant is a valid wpa_supplicant.conf file for Raspbian.
//
// On Raspbian with package raspberrypi-net-mods installed (it is installed by
// default on stretch lite), a file /boot/wpa_supplicant.conf will
// automatically be copied to /etc/wpa_supplicant/.
//
// This has two advantages:
// - wifi is enabled sooner in the boot process than when it's setup.sh that
//   does it.
// - the preshared key (passphrase) is stored in hashed form.
const raspberryPiWPASupplicant = `country=%s
ctrl_interface=DIR=/var/run/wpa_supplicant GROUP=netdev
update_config=1

# Generated by https://github.com/periph/bootstrap
network={
	ssid="%s"
	psk=%s
	key_mgmt=WPA-PSK
}
`

var (
	image        img.Image
	sshKey       = flag.String("ssh-key", img.FindPublicKey(), "ssh public key to use")
	email        = flag.String("email", "", "email address to forward root@localhost to")
	wifiCountry  = flag.String("wifi-country", img.GetCountry(), "Country setting for Wifi; affect usable bands")
	wifiSSID     = flag.String("wifi-ssid", "", "wifi ssid")
	wifiPass     = flag.String("wifi-pass", "", "wifi password")
	fiveInches   = flag.Bool("5inch", false, "Enable support for 5\" 800x480 display (Raspbian only)")
	forceUART    = flag.Bool("forceuart", false, "Enable console UART support (Raspbian only)")
	sdCard       = flag.String("sdcard", getDefaultSDCard(), getSDCardHelp())
	timeLocation = flag.String("time", img.GetTimeLocation(), "Location to use to define time")
	postScript   = flag.String("post", "", "Command to run after setup is done")
	v            = flag.Bool("v", false, "log verbosely")
)

// sdCardsFound is the list of SD cards found on the system. Cache the value as
// getting the list may imply shelling out a process, and it's inefficient to
// do it multiple times for the lifetime of this process.
var sdCardsFound = img.ListSDCards()

func init() {
	flag.Var(&image.Manufacturer, "manufacturer", img.ManufacturerHelp())
	flag.Var(&image.Board, "board", img.BoardHelp())
	flag.Var(&image.Distro, "distro", img.DistroHelp())
}

// Utils

func getDefaultSDCard() string {
	if len(sdCardsFound) == 1 {
		return sdCardsFound[0]
	}
	return ""
}

func getSDCardHelp() string {
	if len(sdCardsFound) == 0 {
		return fmt.Sprintf("Path to SDCard; be sure to insert one first")
	}
	if len(sdCardsFound) == 1 {
		return fmt.Sprintf("Path to SDCard")
	}
	return fmt.Sprintf("Path to SDCard; one of %s", strings.Join(sdCardsFound, ","))
}

// copyFile copies src from dst.
func copyFile(dst, src string, mode os.FileMode) error {
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

// Editing EXT4

func modifyEXT4(img string) (bool, error) {
	fmt.Printf("- Modifying image %s\n", img)
	f, err := os.OpenFile(img, os.O_RDWR, 0666)
	if err != nil {
		return false, err
	}
	modified, err := modifyEXT4Inner(f)
	if err2 := f.Close(); err == nil {
		err = err2
	}
	return modified, err
}

type fileDisk struct {
	f    *os.File
	off  int64
	size int64
}

func (f *fileDisk) Close() error {
	return errors.New("abstraction layer error")
}

func (f *fileDisk) Len() int64 {
	return f.size
}

func (f *fileDisk) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > f.size {
		return 0, io.EOF
	}
	return f.f.ReadAt(p, off+f.off)
}

func (f *fileDisk) SectorSize() int {
	return 512
}

func (f *fileDisk) WriteAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > f.size {
		return 0, errors.New("overflow")
	}
	return f.f.WriteAt(p, off+f.off)
}

func modifyEXT4Inner(f *os.File) (bool, error) {
	m, err := mbr.Read(f)
	if err != nil {
		return false, fmt.Errorf("failed to read MBR: %v", err)
	}
	if err = m.Check(); err != nil {
		return false, err
	}
	rootpart := m.GetPartition(2)
	root := &fileDisk{f, int64(rootpart.GetLBAStart() * 512), int64(rootpart.GetLBALen() * 512)}

	// modifyRoot edits the root partition manually.
	//
	// Since on Debian /etc/rc.local is mostly comments, it's large enough to be
	// safely overwritten.
	offset := int64(0)
	prefix := []byte(oldRcLocal)
	buf := make([]byte, 512)
	for ; offset < root.Len(); offset += 512 {
		if _, err = root.ReadAt(buf, offset); err != nil {
			return false, fmt.Errorf("failed to read at offset %d while seaching for /etc/rc.local: %v", offset, err)
		}
		if bytes.Equal(buf[:len(prefix)], prefix) {
			log.Printf("found /etc/rc.local at offset %d", offset)
			break
		}
	}
	if offset >= root.Len() {
		return false, nil
	}
	// TODO(maruel): Keep everything before the "exit 0" before our injected
	// lines.
	content := fmt.Sprintf(denseRcLocal, firstBootArgs())
	copy(buf, content)
	log.Printf("Writing /etc/rc.local:\n%s", buf)
	_, err = root.WriteAt(buf, offset)
	return true, err
}

func firstBootArgs() string {
	args := " -t " + *timeLocation
	if len(*email) != 0 {
		args += " -e " + *email
	}
	if *fiveInches {
		args += " -5"
	}
	if len(*sshKey) != 0 {
		args += " -sk /boot/authorized_keys"
	}
	// For Raspbian, we can dump a /boot/wpa_supplicant.conf that will be picked
	// up automatically.
	if image.Distro != img.Raspbian {
		args += " -wc " + *wifiCountry
		if len(*wifiSSID) != 0 {
			// TODO(maruel): Proper shell escaping.
			args += fmt.Sprintf(" -ws %q", *wifiSSID)
		}
		if len(*wifiPass) != 0 {
			// TODO(maruel): Proper shell escaping.
			args += fmt.Sprintf(" -wp %q", *wifiPass)
		}
	}
	if len(*postScript) != 0 {
		args += " -- /boot/" + filepath.Base(*postScript)
	}
	return args
}

// wpaPSK calculates the hex encoded preshared key for the SSID based on the
// plain text password.
//
// This removes the need to have the plain text wifi passphrase on the SD card.
func wpaPSK(passphrase, ssid string) string {
	return hex.EncodeToString(pbkdf2.Key([]byte(passphrase), []byte(ssid), 4096, 32, sha1.New))
}

// Editing FAT

func setupFirstBoot(boot string) error {
	fmt.Printf("- First boot setup script\n")
	if err := ioutil.WriteFile(filepath.Join(boot, "firstboot.sh"), img.GetSetupSH(), 0755); err != nil {
		return err
	}
	if len(*sshKey) != 0 {
		// This assumes you have properly set your own ssh keys and plan to use them.
		if err := copyFile(filepath.Join(boot, "authorized_keys"), *sshKey, 0644); err != nil {
			return err
		}
	}
	if len(*postScript) != 0 {
		if err := copyFile(filepath.Join(boot, filepath.Base(*postScript)), *postScript, 0755); err != nil {
			return err
		}
	}
	// For Raspbian, we can dump a /boot/wpa_supplicant.conf that will be picked
	// up automatically.
	if image.Distro == img.Raspbian && len(*wifiSSID) != 0 {
		c := fmt.Sprintf(raspberryPiWPASupplicant, *wifiCountry, *wifiSSID, wpaPSK(*wifiPass, *wifiSSID))
		if err := ioutil.WriteFile(filepath.Join(boot, "wpa_supplicant.conf"), []byte(c), 0644); err != nil {
			return err
		}
	}
	return nil
}

// raspbianEnableUART enables console on UART on RPi3.
//
// This is only needed when debugging over serial, mainly to debug issues with
// setup.sh.
//
// https://www.raspberrypi.org/forums/viewtopic.php?f=28&t=141195
func raspbianEnableUART(boot string) error {
	fmt.Printf("- Enabling console on UART on RPi3\n")
	f, err := os.OpenFile(filepath.Join(boot, "config.txt"), os.O_APPEND|os.O_WRONLY, 0666)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(raspberryPi3UART); err != nil {
		return err
	}
	return f.Close()
}

//

func mainImpl() error {
	// Simplify our life on locale not in en_US.
	os.Setenv("LANG", "C")
	// TODO(maruel): Make it usable without root with:
	//   sudo setcap CAP_SYS_ADMIN,CAP_DAC_OVERRIDE=ep __file__
	flag.Parse()
	if !*v {
		log.SetOutput(ioutil.Discard)
	}
	if (*wifiSSID != "") != (*wifiPass != "") {
		return errors.New("use both --wifi-ssid and --wifi-pass")
	}
	if err := image.Check(); err != nil {
		return err
	}
	if image.Distro != img.Raspbian {
		if *fiveInches {
			return errors.New("-5inch only make sense with -distro raspbian")
		}
		if *forceUART {
			return errors.New("-forceuart only make sense with -distro raspbian")
		}
	}
	if *sdCard == "" {
		return errors.New("-sdcard is required")
	}

	if *wifiSSID == "" {
		fmt.Println("Wifi will not be configured!")
	}
	imgpath, err := image.Fetch()
	if err != nil {
		return err
	}
	e := filepath.Ext(imgpath)
	imgmod := imgpath[:len(imgpath)-len(e)] + "-mod" + e
	if err = copyFile(imgmod, imgpath, 0666); err != nil {
		return err
	}
	// TODO(maruel): Recent distros do not have a /etc/rc.local file.
	modified, err := modifyEXT4(imgmod)
	if err != nil {
		return err
	}
	if !modified {
		fmt.Printf("Couldn't modified the image to setup automatically on boot.\n")
		fmt.Printf("You will have to ssh in and run:\n")
		fmt.Printf("  /boot/firstboot.sh%s\n", firstBootArgs())
	}
	fmt.Printf("Warning! This will blow up everything in %s\n\n", *sdCard)
	if runtime.GOOS != "windows" {
		fmt.Printf("This script has minimal use of 'sudo' for 'dd' to format the SDCard\n\n")
	}
	if err = img.Flash(imgmod, *sdCard); err != nil {
		return err
	}

	// Unmount then remount to ensure we get the path.
	if err = img.Umount(*sdCard); err != nil {
		return err
	}
	boot, err := img.Mount(*sdCard, 1)
	if err != nil {
		return err
	}
	if boot == "" {
		return errors.New("failed to mount /boot")
	}
	log.Printf("  /boot mounted as %s\n", boot)

	if err = setupFirstBoot(boot); err != nil {
		return err
	}
	if *forceUART {
		if err = raspbianEnableUART(boot); err != nil {
			return err
		}
	}
	if err = img.Umount(*sdCard); err != nil {
		return err
	}

	fmt.Printf("\nYou can now remove the SDCard safely and boot your micro computer\n")
	fmt.Printf("Connect with:\n")
	fmt.Printf("  ssh -o StrictHostKeyChecking=no %s@%s\n\n", image.DefaultUser(), image.DefaultHostname())
	fmt.Printf("You can follow the update process by either:\n")
	fmt.Printf("- connecting a monitor\n")
	fmt.Printf("- connecting to the serial port\n")
	fmt.Printf("- ssh'ing into the device and running:\n")
	fmt.Printf("    tail -f /var/log/firstboot.log\n")
	return nil
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "\nefe: %s.\n", err)
		os.Exit(1)
	}
}
