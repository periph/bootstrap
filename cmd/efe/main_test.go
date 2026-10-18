// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"periph.io/x/bootstrap/img"
)

func TestModifyRootFS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux loop mount uses Unix mock executables")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	fdisk := "#!/bin/sh\nprintf '%s\\n' '/dev/mock 1 2 2 83 Linux'\n"
	sudo := "#!/bin/sh\ncase \"$1\" in\nmount) mkdir -p \"$5/etc\" ;;\numount) exit 42 ;;\n*) exit 99 ;;\nesac\n"
	for name, content := range map[string]string{"fdisk": fdisk, "sudo": sudo} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := *wifiPass
	*wifiPass = "test-password"
	t.Cleanup(func() { *wifiPass = old })
	err := modifyRootFS("test.img")
	if err == nil || !strings.Contains(err.Error(), "exit status 42") {
		t.Fatalf("unmount failure = %v", err)
	}
	entries, err := filepath.Glob(filepath.Join(dir, "efe-ext4-*", "etc", "rc.local"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("rc.local paths=%v error=%v", entries, err)
	}
	info, err := os.Stat(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("credential-bearing rc.local mode = %o, want 700", info.Mode().Perm())
	}
}

func TestSetupFirstBoot(t *testing.T) {
	gp := t.TempDir()
	t.Setenv("GOPATH", gp)
	source := filepath.Join(gp, "go", "src", "periph.io", "x", "bootstrap")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "setup.sh"), []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldImage, oldKey, oldPost := image, *sshKey, *postScript
	image.Distro, *sshKey, *postScript = img.RaspiOS, "", ""
	t.Cleanup(func() { image, *sshKey, *postScript = oldImage, oldKey, oldPost })
	boot := t.TempDir()
	path := filepath.Join(boot, "user-data")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setupFirstBoot(boot); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("cloud-init user-data mode = %o, want 600", info.Mode().Perm())
	}
}
