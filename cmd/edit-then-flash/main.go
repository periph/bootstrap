// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// edit-then-flash fetches an image, modifies it, then flashes it to an SDCard.
package main // import "periph.io/x/bootstrap/cmd/edit-then-flash"

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/maruel/go-fs"
	"github.com/maruel/go-fs/fat"
	"github.com/rekby/mbr"
	"periph.io/x/bootstrap/img"
)

// oldRcLocal is the start of /etc/rc.local as found on Debian derived
// distributions.
//
// The comments are essentially the free space available to edit the file
// without having to understand EXT4. :)
const oldRcLocal = "#!/bin/sh -e\n#\n# rc.local\n#\n# This script is executed at the end of each multiuser runlevel.\n# Make sure that the script will \"exit 0\" on success or any other\n# value on error.\n#\n# In order to enable or disable this script just change the execution\n# bits.\n#\n# By default this script does nothing.\n"

// denseRcLocal is a 'dense' version of img.RcLocalContent.
const denseRcLocal = "#!/bin/sh -e\nL=/var/log/firstboot.log;if [ ! -f $L ];then /boot/firstboot.sh%s 2>&1|tee $L;fi\n#"

var (
	distro       img.Distro
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

func init() {
	flag.Var(&distro.Manufacturer, "manufacturer", img.ManufacturerHelp())
	flag.Var(&distro.Board, "board", img.BoardHelp())
	// TODO(maruel): flag.StringVar(&distro.Distro, "distro", "", "Specific distro, optional")
}

// Utils

func getDefaultSDCard() string {
	if b := img.ListSDCards(); len(b) == 1 {
		return b[0]
	}
	return ""
}

func getSDCardHelp() string {
	b := img.ListSDCards()
	if len(b) == 0 {
		return fmt.Sprintf("Path to SD card; be sure to insert one first")
	}
	if len(b) == 1 {
		return fmt.Sprintf("Path to SD card")
	}
	return fmt.Sprintf("Path to SD card; one of %s", strings.Join(b, ","))
}

func writeToFile(dst io.Writer, src string) error {
	fs, err := os.Open(src)
	if err != nil {
		return err
	}
	defer fs.Close()
	_, err = io.Copy(dst, fs)
	return err
}

// Editing image code

// raspbianEnableUART enables console on UART on RPi3.
//
// This is only needed when debugging over serial, mainly to debug issues with
// setup.sh.
//
// https://www.raspberrypi.org/forums/viewtopic.php?f=28&t=141195
func raspbianEnableUART(boot fs.Directory) error {
	fmt.Printf("- Enabling console on UART on RPi3\n")
	s, err := boot.AddFile("config.txt")
	if err != nil {
		return err
	}
	s2, err := s.File()
	if err != nil {
		return err
	}
	b, err := ioutil.ReadAll(s2)
	if err != nil {
		return err
	}
	_, err = s2.Write(append(b, []byte(img.RaspberryPi3UART)...))
	return err
}

func modifyImage(img string) error {
	fmt.Printf("- Modifying image %s\n", img)
	f, err := os.OpenFile(img, os.O_RDWR, 0666)
	if err != nil {
		return err
	}
	err = modifyImageInner(f)
	err2 := f.Close()
	if err == nil {
		return err2
	}
	return err
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
	if off+f.off+int64(len(p)) > f.size {
		return 0, io.EOF
	}
	return f.f.ReadAt(p, off+f.off)
}

func (f *fileDisk) SectorSize() int {
	return 512
}

func (f *fileDisk) WriteAt(p []byte, off int64) (int, error) {
	if off+f.off+int64(len(p)) > f.size {
		return 0, errors.New("overflow")
	}
	return f.f.WriteAt(p, off+f.off)
}

func modifyImageInner(f *os.File) error {
	m, err := mbr.Read(f)
	if err != nil {
		return nil
	}
	if err = m.Check(); err != nil {
		return err
	}
	boot := m.GetPartition(1)
	d := &fileDisk{f, int64(boot.GetLBAStart() * 512), int64(boot.GetLBALen() * 512)}
	filesys, err := fat.New(d)
	if err != nil {
		return err
	}
	rootDir, err := filesys.RootDir()
	if err != nil {
		return err
	}
	if err = modifyBoot(rootDir); err != nil {
		return err
	}
	root := m.GetPartition(2)
	d = &fileDisk{f, int64(root.GetLBAStart() * 512), int64(root.GetLBALen() * 512)}
	return modifyRoot(d)
}

func addFile(p fs.Directory, dst, src string) error {
	s, err := p.AddFile(dst)
	if err != nil {
		return err
	}
	s2, err := s.File()
	if err != nil {
		return err
	}
	return writeToFile(s2, src)
}

func writeFile(p fs.Directory, dst string, content []byte) error {
	s, err := p.AddFile(dst)
	if err != nil {
		return err
	}
	s2, err := s.File()
	if err != nil {
		return err
	}
	_, err = s2.Write(content)
	return err
}

func modifyBoot(boot fs.Directory) error {
	if err := writeFile(boot, "firstboot.sh", img.GetSetupSH()); err != nil {
		return err
	}
	if len(*sshKey) != 0 {
		if err := addFile(boot, "authorized_keys", *sshKey); err != nil {
			return err
		}
	}
	if len(*postScript) != 0 {
		if err := addFile(boot, filepath.Base(*postScript), *postScript); err != nil {
			return err
		}
	}
	if *forceUART {
		if err := raspbianEnableUART(boot); err != nil {
			return err
		}
	}
	// TODO(maruel): RaspberryPi != Raspbian.
	if distro.Manufacturer == img.RaspberryPi && len(*wifiSSID) != 0 {
		c := fmt.Sprintf(img.RaspberryPiWPASupplicant, *wifiCountry, *wifiSSID, *wifiPass)
		if err := writeFile(boot, "wpa_supplicant.conf", []byte(c)); err != nil {
			return err
		}
	}
	return nil
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
	// TODO(maruel): RaspberryPi != Raspbian.
	if distro.Manufacturer != img.RaspberryPi {
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

// modifyRoot edits the root partition manually.
//
// Since on Debian /etc/rc.local is mostly comments, it's large enough to be
// safely overwritten.
func modifyRoot(root *fileDisk) error {
	offset := int64(0)
	prefix := []byte(oldRcLocal)
	buf := make([]byte, 512)
	for ; ; offset += 512 {
		if _, err := root.ReadAt(buf, offset); err != nil {
			return err
		}
		if bytes.Equal(buf[:len(prefix)], prefix) {
			log.Printf("found /etc/rc.local at offset %d", offset)
			break
		}
	}
	// TODO(maruel): Keep everything before the "exit 0" before our injected
	// lines.
	content := fmt.Sprintf(denseRcLocal, firstBootArgs())
	copy(buf, content)
	log.Printf("Writing /etc/rc.local:\n%s", buf)
	_, err := root.WriteAt(buf, offset)
	return err
}

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
	if err := distro.Check(); err != nil {
		return err
	}
	if distro.Manufacturer != img.RaspberryPi {
		if *fiveInches {
			return errors.New("-5inch only make sense with -manufacturer raspberrypi")
		}
		if *forceUART {
			return errors.New("-forceuart only make sense with -manufacturer raspberrypi")
		}
	}

	imgpath, err := distro.Fetch()
	if err != nil {
		return err
	}
	e := filepath.Ext(imgpath)
	imgmod := imgpath[:len(imgpath)-len(e)] + "-mod" + e
	if err := img.CopyFile(imgmod, imgpath, 0666); err != nil {
		return err
	}
	if err = modifyImage(imgmod); err != nil {
		return err
	}

	if *sdCard == "" {
		fmt.Printf("No path to SDCard was provided. Please flash %s manually\n", imgmod)
		fmt.Printf("then insert the SDCard into the micro computer.\n")
	} else {
		fmt.Printf("Warning! This will blow up everything in %s\n\n", *sdCard)
		fmt.Printf("This script has minimal use of 'sudo' for 'dd' and modifying the partitions\n\n")
		if err := img.Flash(imgmod, *sdCard); err != nil {
			return err
		}
		fmt.Printf("\nYou can now remove the SDCard safely and boot your micro computer\n")
	}
	fmt.Printf("Connect with:\n")
	fmt.Printf("  ssh -o StrictHostKeyChecking=no %s@%s\n\n", distro.DefaultUser(), distro.DefaultHostname())
	fmt.Printf("You can follow the update process by either:\n")
	fmt.Printf("- connecting a monitor\n")
	fmt.Printf("- connecting to the serial port\n")
	fmt.Printf("- ssh'ing into the device and running:\n")
	fmt.Printf("    tail -f /var/log/firstboot.log\n")
	return nil
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "\nedit-then-flash: %s.\n", err)
		os.Exit(1)
	}
}
