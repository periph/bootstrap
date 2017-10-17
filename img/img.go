// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// Package img implements image related functionality.
package img // import "periph.io/x/bootstrap/img"

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
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

// Run runs a command.
func Run(name string, arg ...string) error {
	log.Printf("Run(%s %s)", name, strings.Join(arg, " "))
	cmd := exec.Command(name, arg...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
