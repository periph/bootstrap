# Bootstrap micro computers

Bootstrapping a micro computer can be tedious. This repository contains Go tools
to help with that specifically to automate the deployment as much as possible
and make the boxes as secure as possible.

This toolbox was written to help manage the
[gohci](https://github.com/periph/gohci) workers, so they can be reflashed
easily in case of tampering. That said, the script are intentionally generic and
reusable.


## Local

Tools meant to be run on your machine are written in [Go](https://golang.org/)
for portability:

- `cmd/flash`: Downloads an image, flashes it to an SD card and modifies it to
  run `setup.sh` upon the first boot. It only runs on linux.
- `cmd/flash-docker`: Downloads an image, modifies a copy to
  run `setup.sh` upon the first boot then optionally flashes it to an SD card.
  It requires docker to be installed.
- `cmd/find-host`: Looks for devices on the local network through mDNS. Note
  that Raspbian Stretch doesn't advertize anymore.


## Flashing an SD card

This example downloads the Raspbian Stretch Lite image and flashes it to an SD
card. You must supply the path to the SD card, generally in the form of
`/dev/sdX` or `/dev/mmcblkN`. This only works on linux for now.

```
go install periph.io/x/bootstrap/cmd/...
flash -manufacturer raspberrypi --wifi <ssid> <pwd> /dev/sdh
```

`flash` takes care of all the steps below on the micro computer's initial boot.

`flash-docker` works by first modifying a copy of the image and only then flash
it. It may be possible to make to work on OSX and Windows.


### Enabling UART

On a Raspberry Pi 3, the console UART is not enabled by default anymore. Specify
`-forceuart` to enable it, then use a serial cable (like
[FT232RL](https://www.adafruit.com/product/70)) to connect the serial pins to
pins 8 and 10 on the header. Then run `screen /dev/ttyUSB0 115200` on your linux
host to connect (or equivalent on other OSes).


## Configuring a micro computer

`setup.sh` is an all-in-one tool that can be used on an already flashed device,
for example on a Beaglebone or a C.H.I.P. which have integrated non-removable
flash.

If you already have a running device and want to run setup on it, use:

```
curl -sSL https://goo.gl/JcTSsH | bash -s -- --wifi-ssid MY_WIFI --wifi-pass WIFI_PASS --email myself@example.com
```

replacing the `WIFI_` values with your Wifi connection, and `myself@example.com`
with your email address so you are alerted whenever `apt-get upgrade` runs.


### Installing Go

To install or update in-place your Go toolchain on any computer:

```
curl -sSL https://goo.gl/JcTSsH | bash -s -- do_golang
```


### Renaming host

This renames the host to `<board>-<id>` where `<board>` is one of `beaglebone`,
`chip`, `odroid` or `raspberrypi` and `<id>` is calculated from either the CPU
serial number or systemd's hostctl 'Machine ID'.

```
curl -sSL https://goo.gl/JcTSsH | bash -s -- do_rename_host
```


### Configure postfix

To forward all emails sent to `root@localhost` via sendmail, use:

```
curl -sSL https://goo.gl/JcTSsH | bash -s -- --email foo@example.com do_sendmail
```

This one is quite useful, as it also enables automatic email upon
unattended-upgrade. This permits to know when something wrong happens on the
worker.


## Modifications

Here's an incomplete list of modications done by `setup.sh`:

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


### Dry run

To get the full list of the operations done by default, run `setup.sh` in dry
run mode on the host itself:

```
curl -sSL https://goo.gl/JcTSsH | bash -s -- --dry-run
```


### Commands list

To get the full list of commands and options available, download the script
first then run with` `--help`:

```
curl -sSL https://goo.gl/JcTSsH -o setup.sh
bash setup.sh --help
```
