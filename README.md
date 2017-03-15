# Tools to bootstrap micro computers

Bootstrapping a micro computer can be tedious. This repository contains 2
tools to help with that specifically:

- `flash.py`: Downloads an image, flashes it to an SD card and modifies it to
  run `setup.sh` upon the first boot.
- `setup.sh`: script to run on an already flashed device.

These were written to help manage the [gohci](https://github.com/periph/gohci)
workers, so they can be reflashed easily in case of tampering but the script are
intentionally generic and reusable.

This is a work in progress.


## Modifications

- Update the hostname to be `$BOARD-$SERIAL[:4]` where the board is the detected
  board and the serial number is gathered from the CPU, failing that from
  systemctl.
- `/etc/motd` is updated to be `Welcome to $HOST`.
- `apt update` & `apt upgrade` are run.
- `$HOME/.ssh/authorized_keys` (or a public key failing that) is copied in the
  home directory of the device.
- Password based ssh is disabled.
- Setup wpa_supplicant when applicable.
- ODROID
  - Root ssh is disabled.
- Raspbian
  - I²C, SPI are enabled.
  - Keyboard layout is set to en_US.
  - Country (for wifi) is set to Canada.
  - ntpdate is installed.
- Go toolchain is installed in `/usr/local/go`.
- `/etc/profile.d/golang.sh` is added to set `$PATH` and `$GOPATH`.
