#!/bin/bash
# Copyright 2016 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by a BSD-style license that can be found in the
# LICENSE file.

# Run as:
#   curl -sSL https://raw.githubusercontent.com/periph/bootstrap/master/setup.sh | bash
#   curl -sSL https://goo.gl/JcTSsH | bash
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

echo "Waiting for network to be up and running"
until ping -c1 www.google.com &>/dev/null; do :; done
echo "Network is UP"

set -eu

function apt_update_install {
  # Try to work around:
  #  WARNING: The following packages cannot be authenticated!
  sudo apt-key update

  sudo apt-get update
  sudo apt-get upgrade -y
  # If you are space constrained, here's the approximative size:
  # git:                 17.7MB
  # ifstat:               3.3MB
  # python:                18MB
  # sysstat:              1.3MB
  # ssh:                  130kB
  # tmux:                 670kB
  # unattended-upgrades: 18.1MB (!)
  # vim:                   28MB (!)
  #
  # curl is missing on odroid.
  # Optional: ifstat python sysstat
  sudo apt-get install -y curl git ssh tmux unattended-upgrades vim
}


function board_detect {
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
  if [ -f /etc/apt/sources.list.d/odroid.list ]; then
    # Fetching from ODROID's primary repository.
    BOARD=odroid
  fi
  if [ $DIST = raspbian ]; then
    BOARD=raspberrypi
  fi
  echo "Detected board: $BOARD"
}


function setup_beaglebone {
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

  sudo tee --append /boot/uEnv.txt > /dev/null <<EOF

# Change made by https://github.com/periph/bootstrap
cape_enable=bone_capemgr.enable_partno=BB-SPIDEV0
EOF
}


function setup_chip {
  echo "Enabling SPI"
  sudo tee /etc/systemd/system/enable_spi.service > /dev/null <<EOF
[Unit]
Description=Enable SPI
After=auditd.service

[Service]
Type=oneshot
Restart=no
ExecStart=/bin/sh -c 'mkdir -p /sys/kernel/config/device-tree/overlays/spi && cp /lib/firmware/nextthingco/chip/sample-spi.dtbo /sys/kernel/config/device-tree/overlays/spi/dtbo'

[Install]
WantedBy=default.target
EOF
  sudo systemctl daemon-reload
  sudo systemctl enable enable_spi
}


function setup_odroid {
  # By default there is not user account. Create one. The main problem is that
  # it means that it is impossible to ssh in until the account is created.
  sudo useradd odroid --password odroid -M --shell /bin/bash \
    -G adm,cdrom,dialout,dip,fax,floppy,plugdev,sudo,tape,video
  echo odroid:odroid | sudo chpasswd

  # /etc/skel won't be copied automatically when the directory already existed,
  # so forcibly do it now.
  sudo cp /etc/skel/.[!.]* /home/odroid
  sudo chown odroid:odroid /home/odroid/.[!.]*
  # This file is created automatically and owned by root.
  rm -rf /home/odroid/resize.log

  # TODO(maruel): Installing avahi-daemon is not sufficient to have it expose
  # _workstation._tcp over mDNS.
  #    sudo apt install -y avahi-daemon

  # TODO(maruel): Do it in cmd/flash.
  echo "Disabling root ssh support"
  sudo sed -i 's/PermitRootLogin yes/PermitRootLogin no/' /etc/ssh/sshd_config
}


function setup_raspberrypi {
  sudo apt -y remove triggerhappy
  sudo apt install -y ntpdate
  # https://github.com/RPi-Distro/raspi-config/blob/master/raspi-config
  # 0 means enabled.
  # Enables SPI0.
  sudo raspi-config nonint do_spi 0
  # Enables I2C1.
  sudo raspi-config nonint do_i2c 0
  sudo raspi-config nonint do_ssh 0
  sudo raspi-config nonint do_camera 0
  echo "raspi-config done"

  # TODO(maruel): This is a bit intense, most users will not want that.
  sudo tee /etc/systemd/system/hdmi_disable.service > /dev/null <<EOF
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

  # For more /boot/config.txt modifications, see:
  # https://github.com/raspberrypi/firmware/blob/master/boot/overlays/README
  # https://www.raspberrypi.org/documentation/configuration/config-txt/

  # On the Raspberry Pi Zero, enable Ethernet over USB. This is extremely
  # useful!
  sudo tee --append /boot/config.txt > /dev/null <<EOF

# Enable ethernet over USB for Raspberry Pi Zero / Zero Wireless.
[pi0]
dtoverlay=dwc2
[all]

EOF

  # Enable SPI1 in addition to SPI0.
  # To enable SPI1 on RPi3, Bluetooth needs to be disabled.
  #echo -e "\n# Enable SPI1:\ndtoverlay=spi1-2cs" | sudo tee --append /boot/config.txt
  #if RPi2 or RPi3; then
  #  echo -e "\n# Disable UART1:\ndtparam=uart1=off" | sudo tee --append /boot/config.txt
  #fi
  #if RPi3; then
  #  echo -e "# Disable Bluetooth:\ndtoverlay=pi3-disable-bt\n" | sudo tee --append /boot/config.txt
  #  sudo systemctl disable hciuart
  #fi
}


function setup_ssh {
  # Assumes there is only one account. This is true for most distros. The value is
  # generally one of: pi, debian, odroid, chip.
  USERNAME="$(ls /home)"

  # Uncomment and put your keys if desired. flash.py already handles this.
  # KEYS='ssh-ed25519 add_here'
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
}


function install_go {
  ### install_go.sh ###

  # Install the Go toolchain.
  # TODO(maruel): Do not run on C.H.I.P. Pro because of lack of space.
  echo "Installing the Go toolchain"
  echo "- Magically figuring out latest version"
  # TODO(maruel): Detect if x86.
  GO_ARCH=armv6l
  GO_OS_NAME=linux
  URL=`curl -sS https://golang.org/dl/ | grep -Po "https://storage\.googleapis\.com/golang/go[0-9.]+${GO_OS_NAME}-${GO_ARCH}.tar.gz" | head -n 1`
  FILENAME=`echo ${URL} | cut -d / -f 5`

  # The non-guesswork version:
  #BASE_URL=https://storage.googleapis.com/golang/
  #GO_VERSION=1.8.3
  #FILENAME=go${GO_VERSION}.${GO_OS_NAME}-${GO_ARCH}.tar.gz
  #URL=${BASE_URL}/${FILENAME}
  echo "- Fetching $URL"
  echo "  as $FILENAME"
  curl -o $FILENAME -sS $URL
  sudo tar -C /usr/local -xzf $FILENAME
  rm $FILENAME
  # We need to set GOPATH and PATH.
  echo 'export GOPATH="$HOME/go"' | sudo tee /etc/profile.d/golang.sh
  echo 'export PATH="$PATH:/usr/local/go/bin:$GOPATH/bin"' | sudo tee --append /etc/profile.d/golang.sh
  sudo chmod 0555 /etc/profile.d/golang.sh
  # TODO(maruel): Optionally go get a few tools?
  echo "- Done"
}


function setup_unattended_upgrade {
  echo "- Setup automatic upgrades"
  # Enable automatic reboot when necessary. We do not want unsafe devices! This
  # requires package unattended-upgrades.
  sudo tee /etc/apt/apt.conf.d/90periph > /dev/null <<EOF
# Generated by https://github.com/periph/bootstrap
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-Time "03:48";
Unattended-Upgrade::Remove-Unused-Dependencies "true";
EOF
  if [ ! -f /etc/apt/apt.conf.d/20auto-upgrades ]; then
    # The equivalent of: sudo dpkg-reconfigure unattended-upgrades
    # odroid has it by default, but raspbian doesn't.
    sudo cp /usr/share/unattended-upgrades/20auto-upgrades /etc/apt/apt.conf.d/20auto-upgrades
  fi
}


function do_rename_host {
  ### rename_host.sh ###

  # Generate a hostname based on the serial number of the CPU with leading zeros
  # trimmed off, it is a constant yet unique value.
  # Get the CPU serial number, otherwise the systemd machine ID.
  SERIAL="$(cat /proc/cpuinfo | grep Serial | cut -d ':' -f 2 | sed 's/^[ 0]\+//')"
  if [ "$SERIAL" = "" ]; then
    SERIAL="$(hostnamectl status | grep 'Machine ID' | cut -d ':' -f 2 | cut -c 2-)"
  fi
  # On ODROID-C1, Serial is 1b00000000000000 and /etc/machine-id is static. Use
  # the eMMC CID register. https://forum.odroid.com/viewtopic.php?f=80&t=3064
  if [ "$SERIAL" = "1b00000000000000" ]; then
    export SERIAL="$(cat /sys/block/mmcblk0/device/cid | cut -c 25- | cut -c -4)"
  fi

  # Cut to keep the last 4 characters. Otherwise this quickly becomes unwieldy.
  # The first characters cannot be used because they matches when buying
  # multiple devices at once. 4 characters of hex encoded digits gives 65535
  # combinations.  Taking in account there will be at most 255 devices on the
  # network subnet, it should be "good enough". Increase to 5 if needed.
  SERIAL="$(echo $SERIAL | sed 's/.*\(....\)/\1/')"

  HOST="$BOARD-$SERIAL"
  echo "- New hostname is: $HOST"
  if [ $BOARD = raspberrypi ]; then
    sudo raspi-config nonint do_hostname $HOST
  else
    #OLD="$(hostname)"
    #sudo sed -i "s/\$OLD/\$HOST/" /etc/hostname
    #sudo sed -i "s/\$OLD/\$HOST/" /etc/hosts
    # It hangs on the CHIP (?)
    sudo hostnamectl set-hostname $HOST
  fi
}


function fix_motd {
  echo "- Changing MOTD"
  echo "Welcome to $HOST" | sudo tee /etc/motd
  if [ -f /etc/update-motd.d/10-help-text ]; then
    # This is just noise.
    sudo chmod -x /etc/update-motd.d/10-help-text
  fi
}


function do_all {
  apt_update_install
  board_detect
  if [ $BOARD = beaglebone ]; then
    setup_beaglebone
  fi
  if [ $BOARD = chip ]; then
    setup_chip
  fi
  if [ $BOARD = odroid ]; then
    setup_odroid
  fi
  if [ $BOARD = raspberrypi ]; then
    setup_raspberrypi
  fi
  setup_ssh
  install_go
  setup_unattended_upgrade
  do_rename_host
  fix_motd
  # Reboot so the device starts advertizing itself with the new host name.
  sudo shutdown -r now
}


# TODO(maruel): Add support for flags to run optional steps or only run one,
# like install_go
# TODO(maruel): Add support for injecting a custom script before rebooting.
do_all
