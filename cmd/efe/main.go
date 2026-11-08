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
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

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
	wifiCountry  = flag.String("wifi-country", "", "Country setting for Wifi; affect usable bands")
	wifiSSID     = flag.String("wifi-ssid", "", "wifi ssid")
	wifiPass     = flag.String("wifi-pass", "", "wifi password")
	fiveInches   = flag.Bool("5inch", false, "Enable support for 5\" 800x480 display (RaspiOS only)")
	forceUART    = flag.Bool("forceuart", false, "Enable console UART support (RaspiOS only)")
	sdCard       = flag.String("sdcard", getDefaultSDCard(), getSDCardHelp())
	timeLocation = flag.String("time", "", "Location to use to define time")
	postScript   = flag.String("post", "", "Command to run after setup is done")
	v            = flag.Bool("v", false, "log verbosely")
)

var sdCardsFound []string

func init() {
	flag.Var(&image.Manufacturer, "manufacturer", img.ManufacturerHelp())
	flag.Var(&image.Board, "board", img.BoardHelp())
	flag.Var(&image.Distro, "distro", img.DistroHelp())
}

// --- helpers ---

func run(ctx context.Context, name string, args ...string) error {
	log.Printf("run(ctx, %s %s)", name, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func raspiOSUserConf(ctx context.Context, username, password string) string {
	c := exec.CommandContext(ctx, "openssl", "passwd", "-6", "-stdin")
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
	return "Path to SDCard; one of " + strings.Join(sdCardsFound, ",")
}

func copyFile(dst, src string, mode os.FileMode) (retErr error) {
	/* #nosec G304 */
	fs, err := os.Open(src)
	if err != nil {
		return err
	}
	/* #nosec G307 */
	defer func() { retErr = errors.Join(retErr, fs.Close()) }()
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
func getEXT4Partition(ctx context.Context, imgPath string) (ext4Part, error) {
	out, err := exec.CommandContext(ctx, "fdisk", "-l", "--", imgPath).Output()
	if err != nil {
		return ext4Part{}, fmt.Errorf("fdisk: %w", err)
	}
	for l := range strings.SplitSeq(string(out), "\n") {
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
func modifyRootFS(ctx context.Context, imgPath string) (retErr error) {
	ext4, err := getEXT4Partition(ctx, imgPath)
	if err != nil {
		return fmt.Errorf("cannot find EXT4 partition: %w", err)
	}
	mnt, err := os.MkdirTemp("", "efe-ext4-")
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, os.Remove(mnt)) }()

	fmt.Printf("- Mounting EXT4 partition (offset %d) at %s\n", ext4.offset, mnt)
	if err := run(ctx, "sudo", "mount", "-o", fmt.Sprintf("loop,offset=%d,rw", ext4.offset), imgPath, mnt); err != nil {
		return fmt.Errorf("mount EXT4: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, run(context.WithoutCancel(ctx), "sudo", "umount", mnt))
	}()

	// Build rc.local. Kept dense to fit within the 512-byte block if the
	// original rc.local was ever present; harmless otherwise.
	rc := "#!/bin/sh -e\nL=/var/log/firstboot.log\nif [ ! -f $L ];then /boot/firmware/firstboot.sh"
	var rcArgs strings.Builder
	for _, a := range firstBootArgs() {
		rcArgs.WriteByte(' ')
		rcArgs.WriteString(shellQuote(a))
	}
	rc += rcArgs.String()
	rc += " 2>&1|tee $L;fi\nexit 0\n"

	p := filepath.Join(mnt, "etc", "rc.local")
	fmt.Printf("- Writing %s\n", p)
	if err := os.WriteFile(p, []byte(rc), 0o600); err != nil {
		return fmt.Errorf("write rc.local: %w", err)
	}
	if err := os.Chmod(p, 0o700); err != nil {
		return fmt.Errorf("make rc.local executable: %w", err)
	}
	return nil
}

// raspiOSNetworkConfig is a cloud-init netplan v2 config for WiFi.
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

// writeBootData replaces credential-bearing boot data with a private file,
// including when an image supplied an existing world-readable template.
func writeBootData(path string, data []byte) (retErr error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".efe-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, err)
		}
	}()
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func setupFirstBoot(ctx context.Context, boot string) error {
	fmt.Printf("- First boot setup script\n")
	setup := img.GetSetupSH(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(boot, "firstboot.sh"), setup, 0o755); err != nil /* #nosec G306 */ {
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
		if err := writeBootData(filepath.Join(boot, "user-data"), cloudInitUserData()); err != nil {
			return err
		}
		// Legacy files for raspberrypi-sys-mods.
		if err := os.WriteFile(filepath.Join(boot, "ssh"), nil, 0o600); err != nil {
			return err
		}
		if uc := raspiOSUserConf(ctx, "pi", "raspberry"); uc != "" {
			if err := writeBootData(filepath.Join(boot, "userconf.txt"), []byte(uc)); err != nil {
				return err
			}
		}
		// nmconnection for setup.sh's do_nmconnection to copy into place.
		if len(*wifiSSID) != 0 {
			nm := fmt.Sprintf(raspiOSNMConnection, *wifiSSID, *wifiSSID, *wifiPass)
			if err := writeBootData(filepath.Join(boot, *wifiSSID+".nmconnection"), []byte(nm)); err != nil {
				return err
			}
		}

		// Cloud-init network-config gives early WiFi before runcmd fires.
		if len(*wifiSSID) != 0 {
			nc := fmt.Sprintf(raspiOSNetworkConfig, *wifiCountry, *wifiSSID, *wifiPass)
			if err := writeBootData(filepath.Join(boot, "network-config"), []byte(nc)); err != nil {
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

func mainImpl(ctx context.Context) (retErr error) {
	_ = os.Setenv("LANG", "C")
	sdCardsFound = img.ListSDCards(ctx)
	*sdCard = getDefaultSDCard()
	*wifiCountry = img.GetCountry(ctx)
	*timeLocation = img.GetTimeLocation(ctx)
	flag.Lookup("sdcard").DefValue = *sdCard
	flag.Lookup("sdcard").Usage = getSDCardHelp()
	flag.Lookup("wifi-country").DefValue = *wifiCountry
	flag.Lookup("time").DefValue = *timeLocation
	if err := ctx.Err(); err != nil {
		return err
	}
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
	imgPath, err := image.Fetch(ctx)
	if err != nil {
		return err
	}

	// Mount the EXT4 partition via loopback, write rc.local.
	if err = modifyRootFS(ctx, imgPath); err != nil {
		return fmt.Errorf("failed to modify root filesystem: %w\nYou will have to ssh in and run:\n  /boot/firstboot.sh %s", err, shellJoin(firstBootArgs()))
	}

	fmt.Printf("Warning! This will blow up everything in %s\n\n", *sdCard)
	if runtime.GOOS != "windows" {
		fmt.Printf("This script has minimal use of 'sudo' for 'dd' to format the SDCard\n\n")
	}
	if err = img.Flash(ctx, imgPath, *sdCard); err != nil {
		return err
	}

	if err = img.Umount(ctx, *sdCard); err != nil {
		return err
	}
	boot, err := img.Mount(ctx, *sdCard, 1)
	if err != nil {
		return err
	}
	if boot == "" {
		return errors.New("failed to mount /boot")
	}
	mounted := true
	defer func() {
		if mounted {
			retErr = errors.Join(retErr, img.Umount(context.WithoutCancel(ctx), *sdCard))
		}
	}()
	log.Printf("  /boot mounted as %s\n", boot)

	if err = setupFirstBoot(ctx, boot); err != nil {
		return err
	}
	if *forceUART {
		if err = raspiosEnableUART(boot); err != nil {
			return err
		}
	}
	mounted = false
	if err = img.Umount(context.WithoutCancel(ctx), *sdCard); err != nil {
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := mainImpl(ctx)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nefe: %s.\n", err)
		os.Exit(1)
	}
}
