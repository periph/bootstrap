#!/bin/sh
# Copyright 2016 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by a BSD-style license that can be found in the
# LICENSE file.

# Run as:
#   curl -sSL https://raw.githubusercontent.com/maruel/bin_pub/master/devices/setup.sh | bash
#
# - Beaglebone:
#   - User/pwd: debian/temppwd
#   - sudo connmanctl services; sudo connmanctl connect wifi...
# - C.H.I.P.:
#   - User/pwd: chip/chip
#   - Flash with http://flash.getchip.com : Choose the Headless image.
#   - Connect with screen /dev/ttyACM0
#   - Make sure you the C.H.I.P. has network access. This simplest is:
#     nmcli device wifi list
#     sudo nmcli device wifi connect '<ssid>' password '<pwd>' ifname wlan0
# - ODROID-C1 with Ubuntu 16.04.1 minimal:
#   - adduser odroid
#   - usermod -a -G sudo odroid
#   - apt install curl
# - Raspbian Jessie:
#   - User/pwd: pi/raspberry
#   - Flash with ./flash_rasbian.sh

set -eu

# Try to work around:
#  WARNING: The following packages cannot be authenticated!
sudo apt-key update

sudo apt-get update
sudo apt-get upgrade -y
# If you are space constrained, here's the approximative size:
# git:    17.7MB
# ifstat:  3.3MB
# python:   18MB
# sysstat: 1.3MB
# ssh:     130kB
# tmux:    670kB
# vim:      28MB (!)
sudo apt-get install -y git ifstat python ssh sysstat tmux vim


# Automatic detection.
# TODO(maruel): It is very brittle, using /proc/device-tree/model would be a
# step in the right direction.
DIST="$(grep '^ID=' /etc/os-release | cut -c 4-)"
BOARD=unknown
if [ -f /etc/dogtag ]; then
  BOARD=beaglebone
fi
if [ -f /etc/chip_build_info.txt ]; then
  BOARD=chip
fi
# TODO(maruel): detect odroid.
if [ $DIST = raspbian ]; then
  BOARD=raspberrypi
fi
echo "Detected board: $BOARD"


if [ $BOARD = beaglebone ]; then
  # The Beaglebone comes with a lot of packages, which fills up the small 4Gb
  # eMMC quickly. Make some space as we won't be using these.
  # Use the following to hunt and kill:
  #   dpkg --get-selections | less
  sudo apt-get remove -y \
    'ruby*' \
    apache2 \
    apache2-bin \
    apache2-data \
    apache2-utils \
    bb-bonescript-installer-beta \
    bb-cape-overlays \
    bb-customizations \
    bb-doc-bone101-jekyll \
    bb-node-red-installer \
    c9-core-installer \
    jekyll \
    nodejs \
    x11-common
    # Removing one these causes the ethernet over USB to fail:
    #rcn-ee-archive-keyring \
    #rcnee-access-point \
    #seeed-wificonfig-installer \
  sudo apt-get purge -y apache2 mysql-common x11-common

  echo "Enabling SPI"
  #git clone https://github.com/beagleboard/bb.org-overlays
  cd /opt/source/bb.org-overlays
  ./dtc-overlay.sh
  ./install.sh

  cat >> /boot/uEnv.txt << EOF

# Change made by https://github.com/periph/bootstrap
cape_enable=bone_capemgr.enable_partno=BB-SPIDEV0
EOF

fi


if [ $BOARD = chip ]; then
  echo "TODO: C.H.I.P."
fi


if [ $BOARD = odroid ]; then
  echo "TODO: O-DROID"
fi


if [ $BOARD = raspberrypi ]; then
  sudo apt -y remove triggerhappy
  sudo apt install -y ntpdate
  # https://github.com/RPi-Distro/raspi-config/blob/master/raspi-config
  # 0 means enabled.
  sudo raspi-config nonint do_spi 0
  sudo raspi-config nonint do_i2c 0
  sudo raspi-config nonint do_ssh 0
  echo "raspi-config done"

  # TODO(maruel): This is a bit intense, most users will not want that.
  cat > /etc/systemd/system/hdmi_disable.service << EOF
[Unit]
Description=Disable HDMI output to lower overall power consumption
After=auditd.service

[Service]
Type=oneshot
Restart=no
# It is only present on Raspbian.
ExecStart=/bin/sh -c '[ -f /opt/vc/bin/tvservice ] && /opt/vc/bin/tvservice -o || true'

[Install]
WantedBy=default.target
EOF
  sudo systemctl daemon-reload
  sudo systemctl enable hdmi_disable

  # Use the us keyboard layout.
  sudo sed -i 's/XKBLAYOUT="gb"/XKBLAYOUT="us"/' /etc/default/keyboard
  # Fix Wifi country settings for Canada.
  sudo raspi-config nonint do_wifi_country CA

  # Switch to en_US.
  sudo sed -i 's/en_GB/en_US/' /etc/locale.gen
  sudo dpkg-reconfigure --frontend=noninteractive locales
  sudo update-locale LANG=en_US.UTF-8
fi


# Obviously don't use that on your own device; that's my keys. :)
# Uncomment and put your keys if desired. flash.py already handles this.
# KEYS='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJKLhs80AouVRKus3NySEpRDwljUDC0V9dyNwhBuo4p6 maruel'
#if [ "${USER:=root}" != "root" ]; then
#  mkdir -p .ssh
#  echo "$KEYS" >>.ssh/authorized_keys
#else
#  mkdir -p /home/$USERNAME/.ssh
#  echo "$KEYS" >>/home/$USERNAME/.ssh/authorized_keys
#  chown -R $USERNAME:$USERNAME /home/$USERNAME/.ssh
#fi


if [ -f /home/$USERNAME/.ssh/authorized_keys ]; then
  echo "Disabling ssh password authentication support"
  sudo sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config
fi


if [ $BOARD=odroid ]; then
  echo "Disabling root ssh support"
  sudo sed -i 's/PermitRootLogin yes/PermitRootLogin no/' /etc/ssh/sshd_config
fi


# Install the Go toolchain.
# TODO(maruel): Do not run on C.H.I.P. Pro because of lack of space.
# TODO(maruel): Magically figure out latest version.
GO_VERSION=1.8
# TODO(maruel): Detect if x86.
# TODO(maruel): Drop /root/update_golang.sh to make upgrading easier?
GO_ARCH=armv6l
GO_OS_NAME=linux
FILENAME=go${GO_VERSION}.${GO_OS_NAME}-${GO_ARCH}.tar.gz
URL=https://storage.googleapis.com/golang/$FILENAME
echo Fetching $URL
wget $URL
sudo tar -C /usr/local -xzf $FILENAME
rm $FILENAME

# We need to set GOPATH and PATH.
echo 'export GOPATH="$HOME/go"' | sudo tee /etc/profile.d/golang.sh
echo 'export PATH="$PATH:/usr/local/go/bin:$GOPATH/bin"' | sudo tee --append /etc/profile.d/golang.sh
sudo chmod 0555 /etc/profile.d/golang.sh
# TODO(maruel): Optionally go get a few tools?


# Generate a hostname based on the serial number of the CPU with leading zeros
# trimmed off, it is a constant yet unique value.
# Get the CPU serial number, otherwise the systemd machine ID.
SERIAL="$(cat /proc/cpuinfo | grep Serial | cut -d ':' -f 2 | sed 's/^[ 0]\+//')"
if [ "$SERIAL" = "" ]; then
  SERIAL="$(hostnamectl status | grep 'Machine ID' | cut -d ':' -f 2 | cut -c 2-)"
fi
# On ODROID, Serial is 1b00000000000000.
if [ "$SERIAL" = "1b00000000000000" ]; then
  SERIAL="$(hostnamectl status | grep 'Machine ID' | cut -d ':' -f 2 | cut -c 2-)"
fi

# Cut to keep the last 4 characters. Otherwise this quickly becomes unwieldy.
# The first characters cannot be used because they matches when buying multiple
# devices at once. 4 characters of hex encoded digits gives 65535 combinations.
# Taking in account there will be at most 255 devices on the network subnet, it
# should be "good enough". Increase to 5 if needed.
SERIAL="$(echo $SERIAL | sed 's/.*\(....\)/\1/')"

HOST="$BOARD-$SERIAL"
echo "- New hostname is: $HOST"
if [ $BOARD = raspberrypi ]; then
  sudo raspi-config nonint do_hostname $HOST
else
  # It hangs on the CHIP (?)
  sudo sed -i "s/$OLD/$HOST/" /etc/hostname
  sudo sed -i "s/$OLD/$HOST/" /etc/hosts
  #sudo hostnamectl set-hostname $HOST
fi


echo "- Changing MOTD"
echo "Welcome to $HOST" | sudo tee /etc/motd
