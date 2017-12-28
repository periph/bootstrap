// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// push cross compiles one or multiple executables and pushes them to a micro
// computer over ssh.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// run is a shorthand for exec.Command().Run().
func run(name string, arg ...string) error {
	c := exec.Command(name, arg...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

type tool int

const (
	none tool = iota
	rsync
	pscp
	scp
)
const toolName = "nonersyncpscpscp"

var toolIndex = [...]uint8{0, 4, 9, 13, 16}

func (i tool) String() string {
	if i < 0 || i >= tool(len(toolIndex)-1) {
		return fmt.Sprintf("tool(%d)", i)
	}
	return toolName[toolIndex[i]:toolIndex[i+1]]
}

func (t tool) push(verbose bool, src, dst string) error {
	switch t {
	case rsync:
		// Push all files via rsync. This is the fastest method.
		if verbose {
			return run("rsync", "--archive", "-v", "--info=progress2", "--compress", src+"/", dst)
		}
		return run("rsync", "--archive", "--info=progress2", "--compress", src+"/", dst)
	case pscp:
		// Push all files via pscp, provided by PuTTY.
		//
		// It is slower than rsync and will fail if one of the destination
		// executable is under use, but it is a reasonable fallback.
		// TODO(maruel): pscp with an alternate name, then plink in to rename the
		// files.
		if verbose {
			return run("pscp", "-v", "-C", "-p", "-r", src, dst)
		}
		return run("pscp", "-C", "-p", "-r", src, dst)
	case scp:
		// Push all files via scp, provided by OpenSSH.
		//
		// It is slower than rsync and will fail if one of the destination
		// executable is under use, but it is a reasonable fallback.
		// TODO(maruel): scp with an alternate name, then ssh in to rename the
		// files.
		if verbose {
			return run("scp", "-v", "-C", "-p", "-r", src, dst)
		}
		return run("scp", "-C", "-p", "-r", src, dst)
	default:
		return errors.New("please make sure at least one of rsync, scp or pscp is in PATH")
	}
}

// detect returns which tool to use.
func detect() tool {
	if _, err := exec.Command("rsync", "--version").CombinedOutput(); err == nil {
		return rsync
	}
	if runtime.GOOS == "windows" {
		if _, err := exec.Command("pscp", "-V").CombinedOutput(); err == nil {
			return pscp
		}
	}
	_, err := exec.Command("scp", "-V").CombinedOutput()
	if err2, ok := err.(*exec.Error); ok && err2.Err == exec.ErrNotFound {
		return none
	}
	return scp
}

// toPkg returns one or multiple packages matching the relpath.
func toPkg(item string) ([]string, error) {
	out, err := exec.Command("go", "list", item).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list %s: %v", item, err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n"), nil
}

// pushInner does the actual work: build then push.
func pushInner(verbose bool, t tool, pkgs []string, host, rel, d string) error {
	exes := make([]string, len(pkgs))
	for i, pkg := range pkgs {
		exes[i] = filepath.Join(d, filepath.Base(pkg))
	}

	// First build everything.
	for i, pkg := range pkgs {
		fmt.Printf("- Building %s\n", pkg)
		if err := run("go", "build", "-v", "-i", "-o", exes[i], pkg); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to build %s\n", pkg)
			return err
		}
	}

	// Then push it all as one swoop.
	dst := fmt.Sprintf("%s:%s", host, rel)
	fmt.Printf("- Pushing %d executables to %s via %s\n", len(pkgs), dst, t)
	return t.push(verbose, d, dst)
}

// push wraps pushInner with a temporary directory.
func push(verbose bool, t tool, items []string, host, rel string) error {
	// First convert the passed strings into real package names.
	var pkgs []string
	for _, item := range items {
		i, err := toPkg(item)
		if err != nil {
			return err
		}
		pkgs = append(pkgs, i...)
	}

	d, err := ioutil.TempDir("", "push")
	if err != nil {
		return err
	}
	err = pushInner(verbose, t, pkgs, host, rel, d)
	if err1 := os.RemoveAll(d); err == nil {
		err = err1
	}
	return err
}

func mainImpl() error {
	goarch := flag.String("goarch", "arm", "GOARCH value to use")
	goos := flag.String("goos", "linux", "GOOS value to use")
	rel := flag.String("rel", "go/bin", "relative directory to push files into")
	host := flag.String("host", os.Getenv("PUSH_HOST"), "host to push to; defaults to content of environment variable PUSH_HOST")
	verbose := flag.Bool("v", false, "verbose output")
	flag.Parse()
	if flag.NArg() == 0 {
		return errors.New("expected argument, try -help")
	}
	if !*verbose {
		log.SetOutput(ioutil.Discard)
	}

	t := detect()
	if t == none {
		return errors.New("Please make sure at least one of rsync, scp or pscp is in PATH")
	}

	// Simplify our life and just set it process wide.
	os.Setenv("GOARCH", *goarch)
	os.Setenv("GOOS", *goos)
	return push(*verbose, t, flag.Args(), *host, *rel)
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "push: %s.\n\nVisit https://github.com/periph/bootstrap#troubleshooting-push for help.\n", err)
		os.Exit(1)
	}
}
