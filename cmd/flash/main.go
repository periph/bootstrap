// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// flash fetches an image, flashes it to an SDCard and modifies it to bootstrap
// automatically.
package main // import "periph.io/x/bootstrap/cmd/flash"

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
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
	"time"
	"unicode"

	"github.com/ulikunitz/xz"
)

type Distro string

func (d *Distro) String() string {
	return string(*d)
}

func (d *Distro) Set(s string) error {
	if _, ok := userMap[Distro(s)]; !ok {
		return fmt.Errorf("unsupported distro")
	}
	*d = Distro(s)
	return nil
}

const (
	// https://docs.getchip.com/chip.html
	chip Distro = "chip"
	// If you find yourself the need to debug over serial, see
	// http://odroid.com/dokuwiki/doku.php?id=en:usb_uart_kit for how to connect.
	odroidC1 Distro = "odroid-c1"
	// https://www.raspberrypi.org/documentation/linux/
	raspbian Distro = "raspbian"
)

// userMap is the default user as 1000:1000 on the distro.
var userMap = map[Distro]string{
	// https://docs.getchip.com/chip.html
	chip: "chip",
	// Ubuntu minimal doesn't come with a user, it is created on first boot.
	// Using 'odroid' to be compatible with the Ubuntu full image.
	// http://odroid.com/dokuwiki/doku.php?id=en:odroid-c1/
	odroidC1: "odroid",
	// https://www.raspberrypi.org/documentation/linux/usage/users.md
	raspbian: "pi",
}

// hostMap is the default hostname on the distro. rename_host.sh will add
// "-XXXX" suffix based on the device's serial number.
var hostMap = map[Distro]string{
	chip:     "chip",
	odroidC1: "odroid",
	raspbian: "raspberrypi",
}

const wpaSupplicant = `
network={
  ssid="%s"
  psk="%s"
}
`
const raspbian5inchesDisplay = `
# Enable support for 800x480 display:
hdmi_group=2
hdmi_mode=87
hdmi_cvt 800 480 60 6 0 0 0

# Enable touchscreen:
# Not necessary on Jessie Lite since it boots in console mode. :)
# Some displays use 22, others 25.
# Enabling this means the SPI bus cannot be used anymore.
#dtoverlay=ads7846,penirq=22,penirq_pull=2,speed=10000,xohms=150
`

const rcLocalContent = `

# The following was added by cmd/flash from
# https://github.com/periph/bootstrap

LOG_FILE=/var/log/firstboot.log
if [ ! -f $LOG_FILE ]; then
  /root/firstboot.sh 2>&1 | tee $LOG_FILE
fi
exit 0
`

var (
	distro     Distro
	sshKey     = flag.String("ssh-key", findPublicKey(), "ssh public key to use")
	wifiSSID   = flag.String("wifi-ssid", "", "wifi ssid")
	wifiPass   = flag.String("wifi-pass", "", "wifi password")
	fiveInches = flag.Bool("5inch", false, "Enable support for 5\" 800x480 display (raspbian only)")
	skipFlash  = flag.Bool("skip-flash", false, "Skip download and flashing, just modify the image")
	sdCard     = flag.String("sdcard", "", "Path to SD card, generally in the form of /dev/sdX or /dev/mmcblkN")
	v          = flag.Bool("v", false, "log verbosely")
	// Internal flags.
	asRoot = flag.Bool("as-root", false, "")
	img    = flag.String("img", "", "")
)

func init() {
	var names []string
	for k := range userMap {
		names = append(names, string(k))
	}
	sort.Strings(names)
	h := fmt.Sprintf("Select the distribution to install: %s", strings.Join(names, ", "))
	flag.Var(&distro, "distro", h)
}

// Utils

func run(name string, arg ...string) error {
	log.Printf("run(%s %s)", name, strings.Join(arg, " "))
	cmd := exec.Command(name, arg...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func capture(name string, arg ...string) (string, error) {
	log.Printf("capture(%s %s)", name, strings.Join(arg, " "))
	out, err := exec.Command(name, arg...).CombinedOutput()
	return string(out), err
}

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

func chownRecursive(path string, uid, gid int) error {
	return filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err == nil {
			err = os.Chown(name, uid, gid)
		}
		return err
	})
}

// Image fetching

// Reads the image listing to find the latest one.
func raspbianGetLatestImageURL() (string, string) {
	// This is where https://downloads.raspberrypi.org/raspbian_lite_latest
	// redirects to.
	const baseImgURL = "https://downloads.raspberrypi.org/raspbian_lite/images/"
	const imgFmt = "raspbian_lite-%s/%s-raspbian-jessie-lite.zip"
	const fileFmt = "%s-raspbian-jessie-lite.img"
	// Use a recent (as of now) default date, it's not a big deal if the image is
	// a bit stale, it'll just take more time to "apt upgrade".
	date := "2017-04-10"
	if r, err := http.DefaultClient.Get(baseImgURL); err == nil {
		defer r.Body.Close()
		// This will be good until 2099.
		re := regexp.MustCompile(`raspbian_lite-(20\d\d-\d\d-\d\d)/`)
		if reply, err := ioutil.ReadAll(r.Body); err == nil {
			if matches := re.FindAllSubmatch(reply, -1); len(matches) != 0 {
				// It's already in sorted order.
				date = string(matches[len(matches)-1][1])
			} else {
				log.Printf("failed to match")
			}
		} else {
			log.Printf("failed to read")
		}
	} else {
		log.Printf("failed to fetch")
	}
	url := baseImgURL + fmt.Sprintf(imgFmt, date, date)
	log.Printf("Raspbian date: %s", date)
	log.Printf("Latest Raspbian: %s", url)
	return url, fmt.Sprintf(fileFmt, date)
}

// fetchImg fetches the distro image remotely.
func fetchImg() (string, error) {
	switch distro {
	case odroidC1:
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
	case raspbian:
		imgurl, imgname := raspbianGetLatestImageURL()
		if f, _ := os.Open(imgname); f != nil {
			fmt.Printf("- Reusing Raspbian Jessie Lite image %s\n", imgname)
			f.Close()
			return imgname, nil
		}
		fmt.Printf("- Fetching %s\n", imgname)
		resp, err := http.DefaultClient.Get(imgurl)
		if err != nil {
			return "", err
		}
		// Read the whole file in memory. This is less than 300Mb. Save to disk if
		// it is too much for your system.
		// TODO(maruel): Progress bar?
		z, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
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
	default:
		// - https://www.armbian.com/download/
		// - https://beagleboard.org/latest-images better to flash then run setup.sh
		//   manually.
		// - https://flash.getchip.com/ better to flash then run setup.sh manually.
		return "", fmt.Errorf("don't know how to fetch distro %s", distro)
	}
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
		txt, _ := capture("/usr/bin/udisksctl", "mount", "-b", p)
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
				if _, err1 := capture("/usr/bin/udisksctl", "unmount", "-f", "-b", m); err == nil {
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

// enable5inches enables non-standard 5" 800x480 display support.
//
// Found one at 23$USD with free shipping on aliexpress.
func enable5inches(root, boot string) error {
	fmt.Printf("- Enabling 5\" display support\n")
	switch distro {
	case raspbian:
		f, err := os.OpenFile(filepath.Join(boot, "config.txt"), os.O_APPEND, 0666)
		if err != nil {
			return err
		}
		if _, err = f.WriteString(raspbian5inchesDisplay); err != nil {
			return err
		}
		return f.Close()
	default:
		return fmt.Errorf("don't know how to enable 5\" display support on distro %s", distro)
	}
}

func setupFirstBoot(root, boot string) error {
	fmt.Printf("- First boot setup script\n")
	if err := copyFile(filepath.Join(root, "root", "firstboot.sh"), "setup.sh", 0755); err != nil {
		return err
	}
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
	content += rcLocalContent
	log.Printf("Writing %q:\n%s", rcLocal, content)
	return ioutil.WriteFile(rcLocal, []byte(content), 0755)
}

func setupSSH(root, boot string) error {
	fmt.Printf("- SSH keys\n")
	// This assumes you have properly set your own ssh keys and plan to use them.
	home := filepath.Join(root, "home", userMap[distro])
	sshDir := filepath.Join(home, ".ssh")
	if _, err := os.Stat(sshDir); os.IsNotExist(err) {
		if err := os.MkdirAll(sshDir, 0755); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := copyFile(filepath.Join(sshDir, "authorized_keys"), *sshKey, 0644); err != nil {
		return err
	}
	// It is possible the user account does not exist at all as some distros
	// (armbian, odroid's ubuntu minimal) do not include a default user.
	// Inconditionally update the ownership.
	// On all (?) distros, the first user is 1000. This is at least true on
	// Raspbian and NextThing's Debian distro.
	if err := chownRecursive(home, 1000, 1000); err != nil {
		return err
	}

	// Force key based authentication since the password is known.
	// This could be done in setup.sh but doing so would mean that the device
	// would have a window where it would be vulnerable to ssh with default
	// credentials. Doing it here means that even the bootstrapping phase is
	// secure.
	//
	// This is annoying for distros without a default user account, which is
	// created a bit later in setup.sh. In this case it is impossible to ssh in
	// until the account is created, as ssh as root is disabled.
	p := filepath.Join(root, "etc", "ssh", "sshd_config")
	content, err := ioutil.ReadFile(p)
	if err != nil {
		return err
	}
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if line == "#PasswordAuthentication yes" {
			lines[i] = "PasswordAuthentication no"
			break
		}
	}
	if err := ioutil.WriteFile(p, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return err
	}

	// https://www.raspberrypi.org/documentation/remote-access/ssh/
	if distro == raspbian {
		f, err := os.Create(filepath.Join(boot, "ssh"))
		if err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
	}
	return nil
}

func setupWifi(root, boot string) error {
	fmt.Printf("- Wifi")
	f, err := os.OpenFile(filepath.Join(root, "etc", "wpa_supplicant", "wpa_supplicant.conf"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(fmt.Sprintf(wpaSupplicant, *wifiSSID, *wifiPass)); err != nil {
		return err
	}
	return f.Close()
}

// flash flashes *img to *sdCard.
func flash() error {
	switch runtime.GOOS {
	case "linux":
		fmt.Printf("- Unmounting\n")
		if err := umount(*sdCard); err != nil {
			return err
		}
		fmt.Printf("- Flashing (takes 2 minutes)\n")
		if err := run("dd", "bs=4M", "if="+*img, "of="+*sdCard); err != nil {
			return err
		}
		fmt.Printf("- Flushing I/O cache\n")
		if err := run("sync"); err != nil {
			return err
		}
		// This is important otherwise the mount afterward may 'see' the old partition
		// table.
		fmt.Printf("- Reloading partition table\n")
		// Wait a bit to try to workaround "Error looking up object for device" when
		// immediately using "/usr/bin/udisksctl mount" after this script.
		if err := run("partprobe"); err != nil {
			return err
		}
		if err := run("sync"); err != nil {
			return err
		}
		time.Sleep(time.Second)
		// Needs suffix 'p' for /dev/mmcblkN but not for /dev/sdX
		p := *sdCard
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
		flash()
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

	if err = setupFirstBoot(root, boot); err != nil {
		return err
	}
	if *fiveInches {
		if err = enable5inches(root, boot); err != nil {
			return err
		}
	}
	if len(*sshKey) != 0 {
		if err = setupSSH(root, boot); err != nil {
			return err
		}
	}
	if len(*wifiSSID) != 0 {
		if err = setupWifi(root, boot); err != nil {
			return err
		}
	}
	// https://www.raspberrypi.org/forums/viewtopic.php?f=28&t=141195
	// enable_uart=1 for RPi?

	switch runtime.GOOS {
	case "linux":
		fmt.Printf("- Unmounting\n")
		if err = run("sync"); err != nil {
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
	fmt.Printf("  ssh -o StrictHostKeyChecking=no %s@%s\n\n", userMap[distro], hostMap[distro])
	fmt.Printf("You can follow the update process by either:\n")
	fmt.Printf("- connecting a monitor\n")
	fmt.Printf("- connecting to the serial port\n")
	fmt.Printf("- ssh'ing into the device and running:\n")
	fmt.Printf("    tail -f /var/log/firstboot.log\n")
	return nil
}

func getHome() string {
	if usr, err := user.Current(); err == nil && len(usr.HomeDir) != 0 {
		return usr.HomeDir
	}
	return os.Getenv("HOME")
}

func findPublicKey() string {
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

func mainAsUser() error {
	gp := os.Getenv("GOPATH")
	if len(gp) == 0 {
		gp = filepath.Join(getHome(), "go")
	} else {
		gp = strings.SplitN(gp, string(os.PathListSeparator), 2)[0]
	}
	rsc := filepath.Join(gp, "src", "periph.io", "x", "bootstrap")
	if err := os.Chdir(rsc); err != nil {
		return fmt.Errorf("failed to cd to %s", rsc)
	}

	imgname, err := fetchImg()
	if err != nil {
		return err
	}
	fmt.Printf("Warning! This will blow up everything in %s\n\n", *sdCard)
	fmt.Printf("This script has minimal use of 'sudo' for 'dd' and modifying the partitions\n\n")
	execname, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := []string{execname, "-as-root", "-distro", string(distro), "-ssh-key", *sshKey, "-img", imgname}
	// Propagate optional flags.
	if *wifiSSID != "" {
		cmd = append(cmd, "--wifi-ssid", *wifiSSID)
	}
	if *wifiPass != "" {
		cmd = append(cmd, "-wifi-pass", *wifiPass)
	}
	if *fiveInches {
		cmd = append(cmd, "-5inch")
	}
	if *skipFlash {
		cmd = append(cmd, "-skip-flash")
	}
	if *v {
		cmd = append(cmd, "-v")
	}
	cmd = append(cmd, "-sdcard", *sdCard)
	//log.Printf("Running sudo %s", strings.Join(cmd, " "))
	return run("sudo", cmd...)
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
	if *asRoot {
		return mainAsRoot()
	}
	return mainAsUser()
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "\nflash: %s.\n", err)
		os.Exit(1)
	}
}
