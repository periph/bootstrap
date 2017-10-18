// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// flash-docker fetches an image, modifies it using Docker then flashes it to
// an SDCard, to bootstrap automatically.
package main // import "periph.io/x/bootstrap/cmd/flash-docker"

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/rekby/mbr"
	"periph.io/x/bootstrap/img"
)

var (
	distro       img.Distro
	container    = flag.String("container", "ubuntu:16.04", "container from docker hub to use")
	sshKey       = flag.String("ssh-key", img.FindPublicKey(), "ssh public key to use")
	email        = flag.String("email", "", "email address to forward root@localhost to")
	wifiCountry  = flag.String("wifi-country", img.GetCountry(), "Country setting for Wifi; affect usable bands")
	wifiSSID     = flag.String("wifi-ssid", "", "wifi ssid")
	wifiPass     = flag.String("wifi-pass", "", "wifi password")
	fiveInches   = flag.Bool("5inch", false, "Enable support for 5\" 800x480 display (Raspbian only)")
	forceUART    = flag.Bool("forceuart", false, "Enable console UART support (Raspbian only)")
	sdCard       = flag.String("sdcard", getDefaultSDCard(), getSDCardHelp())
	timeLocation = flag.String("time", img.GetTimeLocation(), "Location to use to define time")
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

func docker(imgpath string, lbaStart uint32, in string, arg string) (string, error) {
	args := []string{
		"run", "-i", "--rm", "--privileged",
		"-v", filepath.Dir(imgpath) + ":/work", *container,
		"/bin/bash", "-c", fmt.Sprintf("mount -o loop,offset=%d /work/%s /mnt ; %s", lbaStart*512, filepath.Base(imgpath), arg),
	}
	out, err := img.Capture(in, "docker", args...)
	if err != nil {
		return "", fmt.Errorf("Failed to run: docker %s:\n%s\n", strings.Join(args, " "), out)
	}
	return out, err
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
func raspbianEnableUART(imgpath string, lbaStart uint32) error {
	fmt.Printf("- Enabling console on UART on RPi3\n")
	if _, err := docker(imgpath, lbaStart, img.RaspberryPi3UART, "cat >> /mnt/config.txt"); err != nil {
		return err
	}
	return nil
}

func modifyImage(imgname string) error {
	imgpath, err := filepath.Abs(imgname)
	if err != nil {
		return err
	}
	f, err := os.Open(imgpath)
	if err != nil {
		return err
	}
	m, err := mbr.Read(f)
	if err != nil {
		f.Close()
		return nil
	}
	if err = m.Check(); err != nil {
		f.Close()
		return err
	}
	lbaBoot := m.GetPartition(1).GetLBAStart()
	lbaRoot := m.GetPartition(2).GetLBAStart()
	if err = f.Close(); err != nil {
		return err
	}
	if err := modifyBoot(imgpath, lbaBoot); err != nil {
		return err
	}
	return modifyRoot(imgpath, lbaRoot)
}

func modifyBoot(imgpath string, lbaStart uint32) error {
	if _, err := docker(imgpath, lbaStart, "", "cp /work/setup.sh /mnt/firstboot.sh"); err != nil {
		return err
	}
	if len(*sshKey) != 0 {
		b, err := ioutil.ReadFile(*sshKey)
		if err != nil {
			return err
		}
		if _, err := docker(imgpath, lbaStart, string(b), "cat > /mnt/authorized_keys"); err != nil {
			return err
		}
	}
	if *forceUART {
		if err := raspbianEnableUART(imgpath, lbaStart); err != nil {
			return err
		}
	}
	return nil
}

func firstBootArgs() string {
	args := " -t " + *timeLocation + " -wc " + *wifiCountry
	if len(*email) != 0 {
		args += " -e " + *email
	}
	if *fiveInches {
		args += " -5"
	}
	if len(*sshKey) != 0 {
		args += " -sk /boot/authorized_keys"
	}
	if len(*wifiSSID) != 0 {
		// TODO(maruel): Proper shell escaping.
		args += fmt.Sprintf(" -ws %q", *wifiSSID)
	}
	if len(*wifiPass) != 0 {
		// TODO(maruel): Proper shell escaping.
		args += fmt.Sprintf(" -wp %q", *wifiSSID, *wifiPass)
	}
	return args
}

func modifyRoot(imgpath string, lbaStart uint32) error {
	content, err := docker(imgpath, lbaStart, "", "cat /mnt/etc/rc.local")
	if err != nil {
		return err
	}
	// Keep the content of the file, trim the "exit 0" at the end. It is
	// important to keep its content since some distros (odroid) use it to resize
	// the partition on first boot.
	content = strings.TrimRightFunc(content, unicode.IsSpace)
	content = strings.TrimSuffix(content, "exit 0")
	content += fmt.Sprintf(img.RcLocalContent, firstBootArgs())
	log.Printf("Writing /etc/rc.local:\n%s", content)
	_, err = docker(imgpath, lbaStart, content, "cat > /mnt/etc/rc.local")
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

	rsc := img.GetPath()
	if err := os.Chdir(rsc); err != nil {
		return fmt.Errorf("failed to cd to %s", rsc)
	}

	imgname, err := distro.Fetch()
	if err != nil {
		return err
	}
	e := filepath.Ext(imgname)
	imgmod := imgname[:len(imgname)-len(e)] + "-mod" + e
	if err := img.CopyFile(imgmod, imgname, 0666); err != nil {
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
		fmt.Fprintf(os.Stderr, "\nflash-docker: %s.\n", err)
		os.Exit(1)
	}
}
