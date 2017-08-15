// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// find-host finds the Raspberry Pis/ODROID/C.H.I.P. on the network that
// broadcast via mDNS.
package main // import "periph.io/x/bootstrap/cmd/find-host"

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func mainImpl() error {
	// Simplify our life on locale not in en_US.
	os.Setenv("LANG", "C")

	out, err := exec.Command("avahi-browse", "-t", "_workstation._tcp", "-r", "-p").CombinedOutput()
	if err != nil {
		return err
	}
	// '=' eth IPv4 host Workstation local mDNS IP
	var lines [][]string
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "=") {
			continue
		}
		items := strings.Split(line, ";")
		if len(items) > 2 && items[2] == "IPv4" {
			lines = append(lines, items)
		}
	}
	for _, line := range lines {
		fmt.Printf("%s: %s\n", line[6], line[7])
	}
	return nil
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "\nfind-host: %s.\n", err)
		os.Exit(1)
	}
}
