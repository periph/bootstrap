// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// efe automates flashing self-setup OS image to micro-computers.
//
// It fetches an OS image, modifies the EXT4 root partition to run firstboot.sh
// on initial boot, flashes it to an SDCard, then writes configuration files to
// the FAT32 boot partition.
//
// The EXT4 partition is mounted via loopback to write /etc/rc.local, which
// systemd's rc-local.service picks up on first boot.
package main // import "periph.io/x/bootstrap/cmd/efe"

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"periph.io/x/bootstrap/img"
)

// raspberryPi3UART is appended to config.txt to enable UART on RPi3+.
const raspberryPi3UART = `

# Enable console on UART on RPi3
# https://www.raspberrypi.org/forums/viewtopic.php?f=28&t=141195
[pi3]
enable_uart=1
[all]
`

// raspiOSNMConnection is a NetworkManager connection profile.
const raspiOSNMConnection = `[connection]
id=%s
type=wifi
autoconnect=true

[wifi]
mode=infrastructure
ssid=%s

[wifi-security]
key-mgmt=wpa-psk
psk=%s

[ipv4]
method=auto
`

var (
	image        img.Image
	sshKey       = flag.String("ssh-key", img.FindPublicKey(), "ssh public key to use")
	email        = flag.String("email", "", "email address to forward root@localhost to")
	wifiCountry  = flag.String("wifi-country", img.GetCountry(), "Country setting for Wifi; affect usable bands")
	wifiSSID     = flag.String("wifi-ssid", "", "wifi ssid")
	wifiPass     = flag.String("wifi-pass", "", "wifi password")
	fiveInches   = flag.Bool("5inch", false, "Enable support for 5\" 800x480 display (RaspiOS only)")
	forceUART    = flag.Bool("forceuart", false, "Enable console UART support (RaspiOS only)")
	sdCard       = flag.String("sdcard", getDefaultSDCard(), getSDCardHelp())
	timeLocation = flag.String("time", img.GetTimeLocation(), "Location to use to define time")
	postScript   = flag.String("post", "", "Command to run after setup is done")
	v            = flag.Bool("v", false, "log verbosely")
)

var sdCardsFound = img.ListSDCards()

func init() {
	flag.Var(&image.Manufacturer, "manufacturer", img.ManufacturerHelp())
	flag.Var(&image.Board, "board", img.BoardHelp())
	flag.Var(&image.Distro, "distro", img.DistroHelp())
}

// --- helpers ---

func run(name string, args ...string) error {
	log.Printf("run(%s %s)", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func raspiOSUserConf(username, password string) string {
	c := exec.Command("openssl", "passwd", "-6", "-stdin")
	c.Stdin = strings.NewReader(password)
	out, err := c.Output()
	if err != nil {
		log.Printf("openssl passwd failed: %v", err)
		return ""
	}
	return username + ":" + strings.TrimSpace(string(out))
}

func getDefaultSDCard() string {
	if len(sdCardsFound) == 1 {
		return sdCardsFound[0]
	}
	return ""
}

func getSDCardHelp() string {
	if len(sdCardsFound) == 0 {
		return "Path to SDCard; be sure to insert one first"
	}
	if len(sdCardsFound) == 1 {
		return "Path to SDCard"
	}
	return fmt.Sprintf("Path to SDCard; one of %s", strings.Join(sdCardsFound, ","))
}

func copyFile(dst, src string, mode os.FileMode) error {
	/* #nosec G304 */
	fs, err := os.Open(src)
	if err != nil {
		return err
	}
	/* #nosec G307 */
	defer fs.Close()
	/* #nosec G304 */
	fd, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fd, fs); err != nil {
		_ = fd.Close()
		return err
	}
	return fd.Close()
}

// shellQuote wraps s in single quotes. If s contains a single quote, double
// quotes are used instead.
func shellQuote(s string) string {
	if strings.Contains(s, "'") {
		return `"` + s + `"`
	}
	return "'" + s + "'"
}

// shellJoin joins arguments into a shell-command string for display.
func shellJoin(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(shellQuote(a))
	}
	return b.String()
}

// --- firstboot args ---

// firstBootArgs returns command-line arguments for firstboot.sh / setup.sh.
func firstBootArgs() []string {
	args := []string{"-t", *timeLocation}
	if len(*email) != 0 {
		args = append(args, "-e", *email)
	}
	if *fiveInches {
		args = append(args, "-5")
	}
	if len(*sshKey) != 0 {
		args = append(args, "-sk", "/boot/firmware/authorized_keys")
	}
	if image.Distro != img.RaspiOS && image.Distro != img.RaspiOS64 {
		args = append(args, "-wc", *wifiCountry)
	}
	if len(*wifiSSID) != 0 {
		args = append(args, "-ws", *wifiSSID)
	}
	if len(*wifiPass) != 0 {
		args = append(args, "-wp", *wifiPass)
	}
	if len(*postScript) != 0 {
		args = append(args, "--", "/boot/"+filepath.Base(*postScript))
	}
	return args
}

// --- EXT4 modification ---

type ext4Part struct {
	offset int64
	size   int64
}

// getEXT4Partition returns the offset (in bytes) and size of the first Linux
// (type 83) partition in a disk image, using fdisk.
func getEXT4Partition(imgPath string) (ext4Part, error) {
	out, err := exec.Command("fdisk", "-l", imgPath).Output()
	if err != nil {
		return ext4Part{}, fmt.Errorf("fdisk: %w", err)
	}
	for _, l := range strings.Split(string(out), "\n") {
		fields := strings.Fields(l)
		if len(fields) >= 5 && fields[4] == "83" {
			start, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				continue
			}
			size, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil {
				continue
			}
			return ext4Part{offset: start * 512, size: size * 512}, nil
		}
	}
	return ext4Part{}, errors.New("no Linux (83) partition found")
}

// modifyRootFS mounts the image's EXT4 partition via loopback and writes
// /etc/rc.local so that systemd runs firstboot.sh on boot.
func modifyRootFS(imgPath string) error {
	ext4, err := getEXT4Partition(imgPath)
	if err != nil {
		return fmt.Errorf("cannot find EXT4 partition: %w", err)
	}
	mnt, err := os.MkdirTemp("", "efe-ext4-")
	if err != nil {
		return err
	}
	defer os.Remove(mnt)

	fmt.Printf("- Mounting EXT4 partition (offset %d) at %s\n", ext4.offset, mnt)
	if err := run("sudo", "mount", "-o", fmt.Sprintf("loop,offset=%d,rw", ext4.offset), imgPath, mnt); err != nil {
		return fmt.Errorf("mount EXT4: %w", err)
	}
	defer func() {
		run("sudo", "umount", mnt)
	}()

	// Build rc.local. Kept dense to fit within the 512-byte block if the
	// original rc.local was ever present; harmless otherwise.
	rc := "#!/bin/sh -e\nL=/var/log/firstboot.log\nif [ ! -f $L ];then /boot/firmware/firstboot.sh"
	for _, a := range firstBootArgs() {
		rc += " " + shellQuote(a)
	}
	rc += " 2>&1|tee $L;fi\nexit 0\n"

	p := filepath.Join(mnt, "etc", "rc.local")
	fmt.Printf("- Writing %s\n", p)
	if err := os.WriteFile(p, []byte(rc), 0o755); err != nil {
		return fmt.Errorf("write rc.local: %w", err)
	}
	return nil
}// raspiOSNetworkConfig is a cloud-init netplan v2 config for WiFi.
const raspiOSNetworkConfig = `network:
  version: 2
  wifis:
    wlan0:
      dhcp4: true
      optional: true
      regulatory-domain: %s
      access-points:
        "%s":
          password: "%s"
`



func setupFirstBoot(boot string) error {
	fmt.Printf("- First boot setup script\n")
	if err := os.WriteFile(filepath.Join(boot, "firstboot.sh"), img.GetSetupSH(), 0o755); err != nil /* #nosec G306 */ {
		return err
	}
	if len(*sshKey) != 0 {
		if err := copyFile(filepath.Join(boot, "authorized_keys"), *sshKey, 0o644); err != nil {
			return err
		}
	}
	if len(*postScript) != 0 {
		if err := copyFile(filepath.Join(boot, filepath.Base(*postScript)), *postScript, 0o755); err != nil {
			return err
		}
	}
	if image.Distro == img.RaspiOS || image.Distro == img.RaspiOS64 {
		// Cloud-init runcmd as a safety net alongside rc.local.
		if err := os.WriteFile(filepath.Join(boot, "user-data"), cloudInitUserData(), 0o644); err != nil {
			return err
		}
		// Legacy files for raspberrypi-sys-mods.
		if err := os.WriteFile(filepath.Join(boot, "ssh"), nil, 0o644); err != nil {
			return err
		}
		if uc := raspiOSUserConf("pi", "raspberry"); uc != "" {
			if err := os.WriteFile(filepath.Join(boot, "userconf.txt"), []byte(uc), 0o644); err != nil {
				return err
			}
		}
		// nmconnection for setup.sh's do_nmconnection to copy into place.
		if len(*wifiSSID) != 0 {
			nm := fmt.Sprintf(raspiOSNMConnection, *wifiSSID, *wifiSSID, *wifiPass)
			if err := os.WriteFile(filepath.Join(boot, *wifiSSID+".nmconnection"), []byte(nm), 0o600); err != nil {
				return err
			}
		}

		// Cloud-init network-config gives early WiFi before runcmd fires.
		if len(*wifiSSID) != 0 {
			nc := fmt.Sprintf(raspiOSNetworkConfig, *wifiCountry, *wifiSSID, *wifiPass)
			if err := os.WriteFile(filepath.Join(boot, "network-config"), []byte(nc), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func raspiosEnableUART(boot string) error {
	fmt.Printf("- Enabling console on UART on RPi3\n")
	/* #nosec G304 */
	/* #nosec G302 */
	f, err := os.OpenFile(filepath.Join(boot, "config.txt"), os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(raspberryPi3UART); err != nil {
		return err
	}
	return f.Close()
}

// cloudInitUserData writes a cloud-init user-data that runs firstboot.sh.
func cloudInitUserData() []byte {
	var b strings.Builder
	b.WriteString("#cloud-config\n\nruncmd:\n")
	b.WriteString("- /bin/sh -c '/boot/firmware/firstboot.sh")
	for _, a := range firstBootArgs() {
		b.WriteByte(' ')
		b.WriteString(shellQuote(a))
	}
	b.WriteString("'\n- [ systemctl, restart, ssh ]\n")
	return []byte(b.String())
}

// --- main ---

func mainImpl() error {
	_ = os.Setenv("LANG", "C")
	flag.Parse()
	if !*v {
		log.SetOutput(io.Discard)
	}
	if (*wifiSSID != "") != (*wifiPass != "") {
		return errors.New("use both --wifi-ssid and --wifi-pass")
	}
	if err := image.Check(); err != nil {
		return err
	}
	if image.Distro != img.RaspiOS && image.Distro != img.RaspiOS64 {
		if *fiveInches {
			return errors.New("-5inch only make sense with -distro raspios")
		}
		if *forceUART {
			return errors.New("-forceuart only make sense with -distro raspios")
		}
	}
	if *sdCard == "" {
		return errors.New("-sdcard is required")
	}

	if *wifiSSID == "" {
		fmt.Println("Wifi will not be configured!")
	}
	imgPath, err := image.Fetch()
	if err != nil {
		return err
	}

	// Mount the EXT4 partition via loopback, write rc.local.
	if err := modifyRootFS(imgPath); err != nil {
		return fmt.Errorf("failed to modify root filesystem: %w\nYou will have to ssh in and run:\n  /boot/firstboot.sh %s", err, shellJoin(firstBootArgs()))
	}

	fmt.Printf("Warning! This will blow up everything in %s\n\n", *sdCard)
	if runtime.GOOS != "windows" {
		fmt.Printf("This script has minimal use of 'sudo' for 'dd' to format the SDCard\n\n")
	}
	if err = img.Flash(imgPath, *sdCard); err != nil {
		return err
	}

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
		if err = raspiosEnableUART(boot); err != nil {
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
