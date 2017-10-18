// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// flash-then-edit fetches an image, flashes it to an SDCard, then modifies it.
package main // import "periph.io/x/bootstrap/cmd/flash-then-edit"

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	"periph.io/x/bootstrap/img"
)

var (
	distro       img.Distro
	sshKey       = flag.String("ssh-key", img.FindPublicKey(), "ssh public key to use")
	email        = flag.String("email", "", "email address to forward root@localhost to")
	wifiCountry  = flag.String("wifi-country", img.GetCountry(), "Country setting for Wifi; affect usable bands")
	wifiSSID     = flag.String("wifi-ssid", "", "wifi ssid")
	wifiPass     = flag.String("wifi-pass", "", "wifi password")
	fiveInches   = flag.Bool("5inch", false, "Enable support for 5\" 800x480 display (Raspbian only)")
	forceUART    = flag.Bool("forceuart", false, "Enable console UART support (Raspbian only)")
	skipFlash    = flag.Bool("skip-flash", false, "Skip download and flashing, just modify the image")
	sdCard       = flag.String("sdcard", getDefaultSDCard(), getSDCardHelp())
	timeLocation = flag.String("time", img.GetTimeLocation(), "Location to use to define time")
	v            = flag.Bool("v", false, "log verbosely")
	// Internal flags.
	asRoot  = flag.Bool("as-root", false, "")
	imgFlag = flag.String("img", "", "")
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

func chownRecursive(path string, uid, gid int) error {
	return filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err == nil {
			err = os.Chown(name, uid, gid)
		}
		return err
	})
}

//

// mount mounts a partition and returns the mount path.
func mount(p string) (string, error) {
	fmt.Printf("- Mounting %s\n", p)
	switch runtime.GOOS {
	case "linux":
		// "Mounted /dev/sdh2 at /media/<user>/<GUID>."
		re1 := regexp.MustCompile(`Mounted (?:[^ ]+) at ([^\\]+)\..*`)
		// "Error mounting /dev/sdh2: GDBus.Error:org.freedesktop.UDisks2.Error.AlreadyMounted: Device /dev/sdh2"
		// "is already mounted at `/media/<user>/<GUID>'.
		re2 := regexp.MustCompile(`is already mounted at ` + "`" + `([^\']+)\'`)
		txt, _ := img.Capture("/usr/bin/udisksctl", "mount", "-b", p)
		if match := re1.FindStringSubmatch(txt); len(match) != 0 {
			return match[1], nil
		}
		if match := re2.FindStringSubmatch(txt); len(match) != 0 {
			return match[1], nil
		}
		return "", fmt.Errorf("failed to mount %q: %q", p, txt)
	default:
		return "", errors.New("mount() is not implemented on this OS")
	}
}

// umount unmounts all the partitions on disk 'p'.
func umount(p string) error {
	switch runtime.GOOS {
	case "linux":
		matches, err := filepath.Glob(p + "*")
		if err != nil {
			return err
		}
		sort.Strings(matches)
		for _, m := range matches {
			if m != p {
				log.Printf("- Unmounting %s", m)
				if _, err1 := img.Capture("/usr/bin/udisksctl", "unmount", "-f", "-b", m); err == nil {
					err = err1
				}
			}
		}
		return nil
	default:
		return errors.New("umount() is not implemented on this OS")
	}
}

// Editing image code

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
	if _, err = f.WriteString(img.RaspberryPi3UART); err != nil {
		return err
	}
	return f.Close()
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

func setupFirstBoot(boot, root string) error {
	fmt.Printf("- First boot setup script\n")
	if err := img.CopyFile(filepath.Join(boot, "firstboot.sh"), "setup.sh", 0755); err != nil {
		return err
	}

	// TODO(maruel): Edit /etc/rc.local directly in the disk image. Since on
	// Debian /etc/rc.local is mostly comments, it's likely large enough to be
	// safely overwritten.
	// https://github.com/periph/bootstrap/issues/1
	// Note: To debug firstboot,sh, comment out the following lines, then login
	// at the console and run the script manually.
	rcLocal := filepath.Join(root, "etc", "rc.local")
	b, err := ioutil.ReadFile(rcLocal)
	if err != nil {
		return err
	}
	// Keep the content of the file, trim the "exit 0" at the end. It is
	// important to keep its content since some distros (odroid) use it to resize
	// the partition on first boot.
	content := strings.TrimRightFunc(string(b), unicode.IsSpace)
	content = strings.TrimSuffix(content, "exit 0")
	if len(*sshKey) != 0 {
		fmt.Printf("- SSH keys\n")
		// This assumes you have properly set your own ssh keys and plan to use them.
		if err := img.CopyFile(filepath.Join(boot, "authorized_keys"), *sshKey, 0644); err != nil {
			return err
		}
	}
	content += fmt.Sprintf(img.RcLocalContent, firstBootArgs())
	log.Printf("Writing %q:\n%s", rcLocal, content)
	return ioutil.WriteFile(rcLocal, []byte(content), 0755)
}

// flash flashes imgPath to dst.
func flash(imgPath, dst string) error {
	switch runtime.GOOS {
	case "linux":
		fmt.Printf("- Unmounting\n")
		if err := umount(dst); err != nil {
			return err
		}
		if err := img.Flash(imgPath, dst); err != nil {
			return err
		}
		// This is important otherwise the mount afterward may 'see' the old partition
		// table.
		fmt.Printf("- Reloading partition table\n")
		// Wait a bit to try to workaround "Error looking up object for device" when
		// immediately using "/usr/bin/udisksctl mount" after this script.
		if err := img.Run("partprobe"); err != nil {
			return err
		}
		if err := img.Run("sync"); err != nil {
			return err
		}
		time.Sleep(time.Second)
		// Needs suffix 'p' for /dev/mmcblkN but not for /dev/sdX
		p := dst
		if strings.Contains(p, "mmcblk") {
			p += "p"
		}
		p += "2"
		for {
			if _, err := os.Stat(p); err == nil {
				break
			}
			fmt.Printf(" (still waiting for partition %s to show up)", p)
			time.Sleep(time.Second)
		}
		fmt.Printf("- \n")
		return nil
	default:
		return errors.New("flash() is not implemented on this OS")
	}
}

func mainAsRoot() error {
	if !*skipFlash {
		if err := flash(*imgFlag, *sdCard); err != nil {
			return err
		}
	}
	var root, boot string
	var err error
	switch runtime.GOOS {
	case "linux":
		// Needs 'p' for /dev/mmcblkN but not for /dev/sdX
		p := *sdCard
		if strings.Contains(p, "mmcblk") {
			p += "p"
		}
		if err = umount(*sdCard); err != nil {
			return err
		}
		boot, err = mount(p + "1")
		if err != nil {
			return err
		}
		fmt.Printf("  /boot mounted as %s\n", boot)
		root, err = mount(p + "2")
		if err != nil {
			return err
		}
		fmt.Printf("  / mounted as %s\n", root)
	default:
		return errors.New("flash() is not implemented on this OS")
	}

	if err = setupFirstBoot(boot, root); err != nil {
		return err
	}
	if *forceUART {
		if err = raspbianEnableUART(boot); err != nil {
			return err
		}
	}

	switch runtime.GOOS {
	case "linux":
		fmt.Printf("- Unmounting\n")
		if err = img.Run("sync"); err != nil {
			return err
		}
		if err = umount(*sdCard); err != nil {
			return err
		}
	default:
		return errors.New("flash() is not implemented on this OS")
	}
	fmt.Printf("\nYou can now remove the SDCard safely and boot your Micro computer\n")
	fmt.Printf("Connect with:\n")
	fmt.Printf("  ssh -o StrictHostKeyChecking=no %s@%s\n\n", distro.DefaultUser(), distro.DefaultHostname())
	fmt.Printf("You can follow the update process by either:\n")
	fmt.Printf("- connecting a monitor\n")
	fmt.Printf("- connecting to the serial port\n")
	fmt.Printf("- ssh'ing into the device and running:\n")
	fmt.Printf("    tail -f /var/log/firstboot.log\n")
	return nil
}

func mainAsUser() error {
	rsc := img.GetPath()
	if err := os.Chdir(rsc); err != nil {
		return fmt.Errorf("failed to cd to %s", rsc)
	}

	imgpath, err := distro.Fetch()
	if err != nil {
		return err
	}
	fmt.Printf("Warning! This will blow up everything in %s\n\n", *sdCard)
	fmt.Printf("This script has minimal use of 'sudo' for 'dd' and modifying the partitions\n\n")
	execname, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := []string{
		execname, "-as-root",
		"-manufacturer", distro.Manufacturer.String(),
		"-board", distro.Board.String(),
		//"-distro", distro.Distro,
		"-ssh-key", *sshKey,
		"-img", imgpath, "-wifi-country", *wifiCountry, "-time", *timeLocation,
	}
	// Propagate optional flags.
	if *wifiSSID != "" {
		cmd = append(cmd, "--wifi-ssid", *wifiSSID)
		cmd = append(cmd, "-wifi-pass", *wifiPass)
	}
	if *email != "" {
		cmd = append(cmd, "-email", *email)
	}
	if *fiveInches {
		cmd = append(cmd, "-5inch")
	}
	if *skipFlash {
		cmd = append(cmd, "-skip-flash")
	}
	if *forceUART {
		cmd = append(cmd, "-forceuart")
	}
	if *v {
		cmd = append(cmd, "-v")
	}
	cmd = append(cmd, "-sdcard", *sdCard)
	//log.Printf("Running sudo %s", strings.Join(cmd, " "))
	return img.Run("sudo", cmd...)
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
	if len(*sdCard) == 0 {
		return errors.New("-sdcard is required")
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
	if *asRoot {
		return mainAsRoot()
	}
	return mainAsUser()
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "\nflash-then-edit: %s.\n", err)
		os.Exit(1)
	}
}
