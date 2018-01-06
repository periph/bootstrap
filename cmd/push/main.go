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

func (t tool) push(verbose bool, src string, pkgs []string, host, rel string) error {
	dst := fmt.Sprintf("%s:%s", host, rel)
	var args []string
	switch t {
	case rsync:
		// Push all files via rsync. This is the fastest method.
		args = []string{"--archive", "--info=progress2", "--compress", src + "/", dst}
		if verbose {
			args = append([]string{"-v"}, args...)
		}
	case pscp, scp:
		// Push all files via pscp/scp, provided by PuTTY/OpenSSH.
		//
		// It is slower than rsync and will fail if one of the destination
		// executable is under use, but it is a reasonable fallback.
		// TODO(maruel): pscp/scp with an alternate name, then plink/ssh in to
		// rename the files.
		args = []string{"-C", "-p", "-r"}
		for _, pkg := range pkgs {
			args = append(args, filepath.Join(src, filepath.Base(pkg)))
		}
		if verbose {
			args = append([]string{"-v"}, args...)
		}
		args = append(args, dst)
	default:
		return errors.New("please make sure at least one of rsync, scp or pscp is in PATH")
	}
	if err := run(t.String(), args...); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// On Windows, the +x bit is lost, so we are required to ssh in to change
		// the file mode.
		args = []string{host, "chmod", "+x"}
		for _, pkg := range pkgs {
			args = append(args, filepath.Join(rel, filepath.Base(pkg)))
		}
		switch t {
		case rsync, scp:
			return run("ssh", args...)
		case pscp:
			return run("plink", args...)
		}
	}
	return nil

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
	out, err := exec.Command("go", "list", item).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return nil, fmt.Errorf("failed to list package %q: %v\n%s", item, err, s)
	}
	return strings.Split(s, "\n"), nil
}

// pushInner does the actual work: build then push.
func pushInner(verbose, prebuild bool, t tool, pkgs []string, host, rel, d string) error {
	// First build everything.
	for _, pkg := range pkgs {
		fmt.Printf("- Building %s\n", pkg)
		args := []string{"build", "-v", "-o", filepath.Join(d, filepath.Base(pkg))}
		if prebuild {
			args = append(args, "-i")
		}
		args = append(args, pkg)
		if err := run("go", args...); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to build %s\n", pkg)
			return err
		}
	}

	// Then push it all as one swoop.
	fmt.Printf("- Pushing %d executables to %s in %s via %s\n", len(pkgs), rel, host, t)
	return t.push(verbose, d, pkgs, host, rel)
}

// push wraps pushInner with a temporary directory.
func push(verbose, prebuild bool, t tool, items []string, host, rel string) error {
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
	err = pushInner(verbose, prebuild, t, pkgs, host, rel, d)
	if err1 := os.RemoveAll(d); err == nil {
		err = err1
	}
	return err
}

// canPrebuild returns if the "-i" flag can be used when running "go build".
//
// p must be "${GOROOT}/pkg/${GOOS}_${GOARCH}"
func canPrebuild(p string) bool {
	i, err := os.Stat(p)
	if os.IsNotExist(err) {
		// Try the upper directory.
		p = filepath.Dir(p)
		i, err = os.Stat(p)
		if err != nil {
			log.Printf("os.Stat(%q): %v", p, err)
			return false
		}
	} else if err != nil {
		// "${GOROOT}/pkg" doesn't exist.
		log.Printf("os.Stat(%q): %v", p, err)
		return false
	}
	if !i.IsDir() {
		log.Printf("%q is not a directory!", p)
		return false
	}
	// os.OpenFile(p, os.O_RDWR, 0777) doesn't work. unix.Access() is well, only
	// on Unix. For now use a sure way to figure it out.
	f, err := ioutil.TempFile(p, "push")
	if err != nil {
		return false
	}
	name := f.Name()
	if err = f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: Failed to close: %s: %v\n", name, err)
	}
	if err = os.Remove(name); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: Failed to remove: %s: %v\n", name, err)
	}
	return true
}

func mainImpl() error {
	goarch := flag.String("goarch", "arm", "GOARCH value to use")
	goarm := flag.String("goarm", "6", "GOARM value to use")
	goos := flag.String("goos", "linux", "GOOS value to use")
	rel := flag.String("rel", ".", "directory on remote host to push files into")
	host := flag.String("host", os.Getenv("PUSH_HOST"), "host to push to; defaults to content of environment variable PUSH_HOST")
	verbose := flag.Bool("v", false, "verbose output")
	flag.Parse()
	pkgs := flag.Args()
	if len(pkgs) == 0 {
		fmt.Printf("Note: No argument provided, defaulting to the current directory.\n")
		pkgs = []string{"."}
	}
	if !*verbose {
		log.SetOutput(ioutil.Discard)
	}

	t := detect()
	if t == none {
		return errors.New("Please make sure at least one of rsync, scp or pscp is in PATH")
	}

	p := filepath.Join(runtime.GOROOT(), "pkg", *goos+"_"+*goarch)
	prebuild := canPrebuild(p)
	if !prebuild {
		fmt.Printf("Note: %s is read-only, see https://github.com/periph/bootstrap#pre-building\n", p)
	}

	// Simplify our life and just set it process wide.
	os.Setenv("GOARCH", *goarch)
	os.Setenv("GOARM", *goarm)
	os.Setenv("GOOS", *goos)
	return push(*verbose, prebuild, t, pkgs, *host, *rel)
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "push: %s\n\nVisit https://github.com/periph/bootstrap#troubleshooting-push for help.\n", err)
		os.Exit(1)
	}
}
