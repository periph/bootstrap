# Tools to bootstrap micro computers

Bootstrapping a micro computer can be tedious. This repository contains Go tools
to help with that specifically to automate the deployment as much as possible.


## Local

Tools meant to be run on your machine are written in [Go](https://golang.org/)
for portability:

- `cmd/flash`: Downloads an image, flashes it to an SD card and modifies it to
  run `setup.sh` upon the first boot.
- `cmd/find`: Looks for devices on the local network through mDNS.


### Flashing the micro computer

This example downloads the Raspbian Jessie Lite image and flashes it to an SD
Card. You must supply the path to the SD card, generally in the form of
`/dev/sdX` or `/dev/mmcblkN`. This only works on linux for now.

```
go install periph.io/x/bootstrap/cmd/...
flash --distro raspbian --wifi <ssid> <pwd> /dev/sdh
```

`flash` takes care of all the steps below on the micro computer initial boot.


## On the micro computer

Tools meant to be run on the micro computer itself are written in bash for
compactness:

- `install_go.sh` to install (or inplace upgrade) the latest Go toolchain.
- `rename_host.sh` to rename the host leveraging the serial number of the
  device.
- `setup.sh`: script to run on an already flashed device.

These were written to help manage the [gohci](https://github.com/periph/gohci)
workers, so they can be reflashed easily in case of tampering but the script are
intentionally generic and reusable.


### Setuping a device

If you already have a running device and want to run setup on it, use:
```
curl -sSL https://goo.gl/JcTSsH | bash
```

or a subset:


### Installing Go

To install or update in-place your Go toolchain on the micro computer:

```
curl -sSL https://goo.gl/TFmVMG | bash
```


### Renaming host

This renames the host to `<board>-<id>` where `<board>` is one of `beaglebone`,
`chip`, `odroid` or `raspberrypi` and `<id>` is calculated from either the CPU
serial number or systemd's hostctl 'Machine ID'.

```
curl -sSL https://goo.gl/EkANh0 | bash
```


## Modifications

Here's the list of modications done by `setup.sh`:

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
