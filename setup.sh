#!/bin/bash
# Copyright 2016 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by a BSD-style license that can be found in the
# LICENSE file.

# Run as:
#   curl -sSL https://raw.githubusercontent.com/periph/bootstrap/master/setup.sh | bash
#   curl -sSL https://goo.gl/JcTSsH | bash
#
# Notes:
# - Functions do_* execute system modifications.
# - Other functions have no side effects, except 'run' which runs a command.
# - For board specific changes, see do_setup_BOARDNAME.
#

set -eu


## Board specific functions.


function do_beaglebone {
  echo "- do_beaglebone: Beaglebone specific changes"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # - User/pwd: debian/temppwd

  # Not sure if it should always be done by default.
  #echo "  Enabling SPI0"
  #run config-pin p9.17 spi_cs
  #run config-pin p9.21 spi_
  #run config-pin p9.18 spi_
  #run config-pin p9.22 spi_sclk

  # This is a bit aggressive but this frees up a lot of GPIOs.
# echo "  Disabling HDMI and audio"
#  sudo_append_file /boot/uEnv.txt <<EOF
#    # Change made by https://github.com/periph/bootstrap
#    disable_uboot_overlay_video=1
#    disable_uboot_overlay_audio=1
#EOF
}


function do_beaglebone_trim {
  echo "- do_beaglebone_trim: Aggressively trim Beaglebone specific packages"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # The Beaglebone comes with a lot of packages preinstalled, which fills up the
  # small 4Gb eMMC quickly. Make some space as we won't be using these.
  #
  # This is not done by default because this is quite aggressive. Run manually
  # with:
  #   bash setup.sh do_beaglebone_trim
  #
  # Use the following to hunt and kill:
  #   dpkg --get-selections | less
  #
  # With this, there should be 50% free disk space and 50% free memory.
  run sudo apt remove -y --purge --allow-change-held-packages \
    c9-core-installer
  run sudo rm -rf /opt/cloud9
  run sudo apt remove -y --purge \
    bb-node-red-installer \
    bone101 \
    doc-beaglebone-getting-started \
    gpiod \
    nginx nginx-common nginx-full \
    nodejs \
    roboticscape
  run sudo rm -rf /var/lib/cloud9
  run sudo rm -rf /usr/local/lib/node_modules
  run sudo apt autoremove -y

  # See https://periph.io/platform/beaglebone/ for more information.
  # Disable SoftAp0.
  run sudo systemctl stop bb-wl18xx-wlan0
  run sudo systemctl disable bb-wl18xx-wlan0
  # Disable bluetooth. This reduces the amount of chatter on 2GHz.
  run sudo systemctl stop bb-wl18xx-bluetooth
  run sudo systemctl disable bb-wl18xx-bluetooth
  run sudo systemctl stop bluetooth
  run sudo systemctl disable bluetooth
}


function do_chip {
  echo "- do_chip: C.H.I.P. specific changes"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # Assumption:
  # - Flash with http://flash.getchip.com : Choose the Headless image.
  # - User/pwd: chip/chip
  # - Connect with: screen /dev/ttyACM0

  echo "  Enabling SPI"
  # Note: SPI on C.H.I.P. isn't stable.
  sudo_write_file /etc/systemd/system/enable_spi.service << EOF
    # Generated by https://github.com/periph/bootstrap
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
  run sudo systemctl daemon-reload
  run sudo systemctl enable enable_spi

  # Add udev rules to make GPIO & SPI accessible as user. Cheat a little and
  # make gpio and spi owned by plugdev, instead of creating new dedicated groups
  # like it is done on RaspiOS.
  sudo_write_file /etc/udev/rules.d/50-periph.rules << EOF
    # Generated by https://github.com/periph/bootstrap
    SUBSYSTEM=="spidev", GROUP="plugdev", MODE="0660"
    SUBSYSTEM=="gpio*", PROGRAM="/bin/sh -c '\
      chown -R root:plugdev /sys/class/gpio && chmod -R 770 /sys/class/gpio;\
      chown -R root:plugdev /sys/devices/virtual/gpio && chmod -R 770 /sys/devices/virtual/gpio;\
      chown -R root:plugdev /sys$devpath && chmod -R 770 /sys$devpath\
    '"
EOF
  run sudo udevadm control --reload-rules
  run sudo udevadm trigger
}


function do_odroid {
  echo "- do_odroid: O-DROID C1+ specific changes"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # TODO(maruel): Assumptions:
  # - ODROID-C1 with Ubuntu 16.04.1 minimal:
  #
  # By default there is not user account. Create one. The main problem is that
  # it means that it is impossible to ssh in until the account is created.
  if [ ! -d /home/odroid ]; then
    run sudo useradd odroid --password odroid -M --shell /bin/bash \
      -G adm,cdrom,dialout,dip,fax,floppy,plugdev,sudo,tape,video
    if [ $DRY_RUN -eq 0 ]; then
      echo odroid:odroid | sudo chpasswd
    else
      echo "Dry run: echo odroid:odroid | sudo chpasswd"
    fi

    # /etc/skel won't be copied automatically when the directory already
    # existed, so forcibly do it now.
    run sudo cp /etc/skel/.[!.]* /home/odroid
    run sudo chown odroid:odroid /home/odroid/.[!.]*
    # This file is created automatically and owned by root.
    run rm -rf /home/odroid/resize.log
  fi
}


function do_raspios {
  echo "- do_raspios: RaspiOS specific changes"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # https://www.raspberrypi.com/documentation/computers/configuration.html#setting-up-a-headless-raspberry-pi
  run sudo apt-get -y remove \
    'gcc-4*' 'gcc-5*' 'gcc-6*' 'gcc-7*' \
    libraspberrypi-doc \
    python-rpi.gpio \
    triggerhappy
  run sudo apt-get install -y ntpdate

  echo "  Enable SPI0, I2C1, Camera, ssh"
  # https://github.com/RPi-Distro/raspi-config/blob/master/raspi-config
  # 0 means enabled.
  run sudo raspi-config nonint do_spi 0
  run sudo raspi-config nonint do_i2c 0
  run sudo raspi-config nonint do_ssh 0
  run sudo raspi-config nonint do_camera 0

  if [ $ACTION_SPI1 -eq 1 ]; then
    echo "  Enable SPI1"
    # TODO(maruel): Skip if dtoverlay=spi1 is already present.
    # To enable SPI1 on RPi3, Bluetooth needs to be disabled.
    sudo_append_file /boot/config.txt << EOF
      # Change made by https://github.com/periph/bootstrap
      # Enable SPI1:
      dtoverlay=spi1-2cs
      [pi2]
      dtparam=uart1=off
      [pi3]
      dtparam=uart1=off
      dtoverlay=pi3-disable-bt
      [all]
EOF
    run sudo systemctl disable hciuart
  fi

  if [ $ACTION_5INCH -eq 1 ]; then
    do_5inch
  fi

  # Use the us keyboard layout.
  run sudo sed -i 's/XKBLAYOUT="gb"/XKBLAYOUT="us"/' /etc/default/keyboard

  # Switch to en_US.
  run sudo sed -i 's/en_GB/en_US/' /etc/locale.gen
  run sudo dpkg-reconfigure --frontend=noninteractive locales
  run sudo update-locale LANG=en_US.UTF-8

  # For more /boot/config.txt modifications, see:
  # https://github.com/raspberrypi/firmware/blob/master/boot/overlays/README
  # https://www.raspberrypi.org/documentation/configuration/config-txt/

  # On the Raspberry Pi Zero, enable Ethernet over USB. This is extremely
  # useful!
  sudo_append_file /boot/config.txt << EOF
    # Change made by https://github.com/periph/bootstrap
    # Enable ethernet over USB for Raspberry Pi Zero / Zero Wireless.
    [pi0]
    dtoverlay=dwc2
    [all]
EOF

  # Now necessary on RaspiOS bullseye. We don't really need to keep the same
  # password.
  # https://www.raspberrypi.com/news/raspberry-pi-bullseye-update-april-2022/
  sudo_append_file /boot/userconf.txt << EOF
    pi:$(echo 'raspberry' | openssl passwd -6 -stdin)
EOF
}


function do_5inch {
  echo "- do_5inch: Enable support for 800x480 5 inches HDMI touchscreen"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  sudo_append_file /boot/config.txt << EOF
    # Change made by https://github.com/periph/bootstrap
    # Enable support for 800x480 display:
    hdmi_group=2
    hdmi_mode=87
    hdmi_cvt 800 480 60 6 0 0 0
    # Enable touchscreen:
    # Not necessary on RaspiOS Lite since it boots in console mode. :)
    # Some displays use 22, others 25.
    # Enabling this means the SPI bus cannot be used anymore.
    #dtoverlay=ads7846,penirq=22,penirq_pull=2,speed=10000,xohms=150
EOF
}


function do_raspios_no_hdmi {
  echo "- do_raspios_no_hdmi: Disable HDMI so save a few tens of milliAmp"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  echo "  Disabling HDMI output"
  sudo_write_file /etc/systemd/system/hdmi_disable.service << EOF
    # Generated by https://github.com/periph/bootstrap
    [Unit]
    Description=Disable HDMI output to lower overall power consumption
    After=auditd.service
    [Service]
    Type=oneshot
    Restart=no
    ExecStart=/bin/sh -c '[ -f /opt/vc/bin/tvservice ] && /opt/vc/bin/tvservice -o || true'
    [Install]
    WantedBy=default.target
EOF
  run sudo systemctl daemon-reload
  run sudo systemctl enable hdmi_disable
}


## Generic changes.


function do_apt {
  echo "- do_apt: Run apt-get update & upgrade and install few apps"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # They may fail. For example RaspiOS has an apt-get update upon first boot
  # that may start just before, causing /var/lib/dpkg/lock to be held. This
  # causes the following command to fails, which then
  while ! run sudo DEBIAN_FRONTEND=noninteractive apt-get update; do
    echo "Failed to apt-get update; retrying"
    sleep 1
  done
  while ! run sudo DEBIAN_FRONTEND=noninteractive apt-get -qy upgrade; do
    echo "Failed to apt-get upgrade; retrying"
    sleep 1
  done

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
  # Optional: git ifstat python sysstat
  while ! run sudo DEBIAN_FRONTEND=noninteractive apt-get -qy install curl ssh tmux unattended-upgrades vim; do
    echo "Failed to apt-get install; retrying"
    sleep 1
  done
}


function do_bash_history {
  echo "- do_bash_history: Injects into .bash_history commands the user may want"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # TODO(maruel): We want this to happen as the expected user.
  echo "tail -f /var/log/firstboot.log" >> ~/.bash_history
}


function do_timezone {
  echo "- do_timezone: Changes the timezone to $TIMEZONE"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  sudo timedatectl set-timezone $TIMEZONE
}


function do_ssh {
  echo "- do_ssh: Enable passwordless ssh"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  if [ "${USER:=root}" != "root" ]; then
    mkdir -p .ssh
    if [ -f "$SSH_KEY" ]; then
      # Do not cp to not copy the file attributes.
      cat "$SSH_KEY" >>.ssh/authorized_keys
    fi
  else
    mkdir -p /home/$USERNAME/.ssh
    if [ -f "$SSH_KEY" ]; then
      cat "$SSH_KEY" >>/home/$USERNAME/.ssh/authorized_keys
    fi
    chown -R $USERNAME:$USERNAME /home/$USERNAME/.ssh
  fi

  if [ -f /home/$USERNAME/.ssh/authorized_keys ]; then
    # Only do if there's an authorized key!
    echo "  Disabling ssh password authentication support"
    if [ -d /etc/ssh/sshd_config.d ]; then
      # Debian 11/bullseye and Ubuntu 22.04 have a nicer way.
      echo 'PasswordAuthentication no' | sudo_append_file /etc/ssh/sshd_config.d/no_password.conf
    else
      run sudo sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config
    fi
  fi

  # Some distros (like O-DROID with Ubuntu minimal) enable ssh as root. This is
  # not a good idea. Take no chance and always make sure it's disabled.
  echo "  Disable root ssh support"
  if [ -d /etc/ssh/sshd_config.d ]; then
    # Debian 11/bullseye and Ubuntu 22.04 have a nicer way.
    echo 'PermitRootLogin no' | sudo_append_file /etc/ssh/sshd_config.d/no_root.conf
  else
    run sudo sed -i 's/PermitRootLogin yes/PermitRootLogin no/' /etc/ssh/sshd_config
  fi
}


function do_golang_compile {
  echo "- do_golang_compile: Compiles the latest Go toolchain locally WARNING: untested"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # Build the Go toolchain locally. Bootstrapping is done via the Go debian
  # package. It is old but recent enough to be used to build a recent version.
  #
  # This is necessary on platforms like Armbian on A64 that does not have 32
  # bits userland support.
  # ~/go1.4 is the default GOROOT_BOOTSTRAP value.

  if ! (which git > /dev/null); then
    # git is necessary to checkout the source. A tarball could be used but using
    # git makes it more trivial to upgrade, and git is generally necessary to
    # use 'go get'.
    run sudo DEBIAN_FRONTEND=noninteractive apt-get -qy install git
  fi

  if [ ! -d ~/golang ]; then
    run git clone https://go.googlesource.com/go ~/golang
    run cd ~/golang
  else
    run cd ~/golang
    run git fetch
  fi
  run git checkout "$(git tag | grep "^go" | egrep -v "beta|rc" | tail -n 1)"
  run cd ./src

  if [ -d ~/go1.4 ]; then
    # TODO(maruel): Use GOROOT_FINAL=/usr/local/go ?
    GOROOT_BOOTSTRAP=~/go1.4 run ./make.bash
  elif [ -d /usr/local/go ]; then
    # TODO(maruel): Copy to ~/go1.4 and then /usr/local/go ?
    GOROOT_BOOTSTRAP=/usr/local/go run ./make.bash
  else
    # Temporarily install the golang debian package.
    run sudo apt-get install -y golang
    # TODO(maruel): Use GOROOT_FINAL=/usr/local/go ?
    GOROOT_BOOTSTRAP=/usr/lib/go run ./make.bash
    # Remove the outdated system version.
    run sudo apt-get remove -y golang
    # Copy itself to a backup so the next upgrade is from a more recent version.
    run cp -a ~/golang ~/go1.4
  fi
}


function do_golang {
  echo "- do_golang: Install latest Go toolchain"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  if ! (which git > /dev/null); then
    # git is generally necessary to use 'go get'. Some projects use hg instead
    # but that more rare, so do not install by default.
    run sudo DEBIAN_FRONTEND=noninteractive apt-get -qy install git
  fi

  local GO_ARCH=$(dpkg --print-architecture)
  if [ "$GO_ARCH" = "armhf" ]; then
    GO_ARCH=armv6l
  fi
  local GO_OS_NAME=linux

  if [ "$(getconf LONG_BIT)" = "64" ]; then
    if [ "$GO_ARCH" = "arm" ]; then
      GO_ARCH="arm64"
    fi
  fi

  # Magically figure out latest version for precompiled binaries.
  local -r NEW_VERSION=$(curl -sS https://go.dev/VERSION?m=text)
  echo "  GO_ARCH=${GO_ARCH}  GO_OS_NAME=${GO_OS_NAME} VER=${NEW_VERSION}"

  local -r FILENAME="${NEW_VERSION}.${GO_OS_NAME}-${GO_ARCH}.tar.gz"
  local -r URL="https://dl.google.com/go/${FILENAME}"

  # Guesswork based on grepping the web page:
  #local -r URL=$(curl -sS https://golang.org/dl/ | grep -Po "https://.+\.com/.+/go[0-9.]+${GO_OS_NAME}-${GO_ARCH}.tar.gz" | head -n 1)
  #local -r NEW_VERSION=$(echo $FILENAME | grep -oP '(?<=go)([0-9\.]+[0-9]+)')

  # The non-guesswork version:
  #local -r BASE_URL=https://redirector.gvt1.com/edgedl/go/
  #local -r GO_VERSION=1.14.3
  #local -r FILENAME=go${GO_VERSION}.${GO_OS_NAME}-${GO_ARCH}.tar.gz
  #local -r URL=${BASE_URL}/${FILENAME}

  # If current == new, skip. This permits running this script nightly at minimal
  # cost.
  local CURRENT_VERSION=""
  if (which go > /dev/null); then
    local CURRENT_VERSION=$(go version | grep -oP '(go[0-9\.]+[0-9]+)')
  fi
  if [ "$CURRENT_VERSION" = "$NEW_VERSION" ]; then
    echo "  Current version is already at $CURRENT_VERSION. Skipping."
    return 0
  fi
  if [ "$CURRENT_VERSION" != "" ]; then
    echo "  Replacing previous version $CURRENT_VERSION with $NEW_VERSION"
  fi

  echo "  Fetching $URL"
  echo "    as $FILENAME"
  run curl -L -o $FILENAME -sS $URL
  if [ -d /usr/local/go ]; then
    echo "  Removing previous version in /usr/local/go"
    run sudo rm -rf /usr/local/go
  fi
  echo "  Extracting to /usr/local/go"
  # Filter to only extract 'go' from the tarball.
  # https://github.com/golang/go/issues/29906
  run sudo tar -C /usr/local -xzf $FILENAME go
  run rm $FILENAME
  echo "  Setting /etc/profile.d/golang.sh for GOPATH and PATH"
  sudo_write_file /etc/profile.d/golang.sh << 'EOF'
    # Generated by https://github.com/periph/bootstrap
    export GOPATH="$HOME/go"
    export PATH="$PATH:/usr/local/go/bin:$GOPATH/bin"
EOF
  run sudo chmod 0555 /etc/profile.d/golang.sh
  # TODO(maruel): Optionally go get a few tools?
}


function do_unattended_upgrade {
  echo "- do_unattended_upgrade: Enables automatic nightly apt update & upgrade"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # Enable automatic reboot when necessary. We do not want unsafe devices! This
  # requires package unattended-upgrades.
  sudo_write_file /etc/apt/apt.conf.d/90periph << EOF
    # Generated by https://github.com/periph/bootstrap
    # Enable /etc/cron.daily/apt.
    APT::Periodic::Enable "1";
    # Automatically reboot.
    Unattended-Upgrade::Automatic-Reboot "true";
    Unattended-Upgrade::Automatic-Reboot-Time "03:48";
    # Clean up disk space.
    Unattended-Upgrade::Remove-Unused-Dependencies "true";
    # Makes it a bit slower but safer; it will not corrupt itself if the host is
    # shutdown during installation.
    Unattended-Upgrade::MinimalSteps "true";
    # apt-get autoclean every NN days.
    APT::Periodic::AutocleanInterval "21";
EOF
  if [ ! -f /etc/apt/apt.conf.d/20auto-upgrades ]; then
    # The equivalent of: sudo dpkg-reconfigure unattended-upgrades
    # odroid has it by default, but RaspiOS doesn't.
    run sudo cp /usr/share/unattended-upgrades/20auto-upgrades /etc/apt/apt.conf.d/20auto-upgrades
  fi
}


function do_sendmail {
  echo "- do_sendmail: Enables sending emails + sends email upon apt-get actions"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  if [ "$DEST_EMAIL" = "" ]; then
    echo "  Must specify the email address to forward root@localhost to via --email"
    exit 1
  fi

  # This is needed otherwise postfix will bring a UI.
  echo "postfix postfix/main_mailer_type string 'No Config'" | run sudo debconf-set-selections

  # If you are space constrained, here's the approximative size:
  # bsd-mailx:            3.8MB
  # postfix:              570kB
  run sudo DEBIAN_FRONTEND=noninteractive apt-get install -yq bsd-mailx postfix

  # Enables sending emails over TLS. Because we want our emails to be secure.
  echo "  Configure outgoing emails"
  sudo_write_file /etc/postfix/main.cf << EOF
    # Generated by https://github.com/periph/bootstrap
    # See http://www.postfix.org/postconf.5.html
    # Where to read account aliases, used to map all emails onto one account
    # and then on to a real email address
    alias_maps = hash:/etc/aliases
    # This sets the hostname, which will be used for outgoing email
    myhostname = $HOST
    # This is the mailserver to connect to deliver email
    # NOTE: This must be the MX server for the account you wish to deliver email to
    # or an open relay (but you hopefully won't find one of them). In my case, this
    # is Google's first MX server (which can be found by doing an MX lookup on my
    # domain).
    relayhost = aspmx.l.google.com
    # Do not relay emails.
    inet_interfaces = loopback-only
    # Disable IPv6. See
    # https://blog.dantup.com/2016/04/setting-up-raspberry-pi-raspbian-jessie-to-send-email/
    # for rationale.
    inet_protocols = ipv4
    # Enable encryption and certificate verification.
    smtp_tls_CAfile = /etc/ssl/certs/ca-certificates.crt
    smtp_tls_security_level = verify
    smtp_tls_session_cache_database = btree:\${data_directory}/smtp_scache
    smtp_tls_verify_cert_match = hostname, nexthop, dot-nexthop
EOF

  echo "  Forward root@$HOST to $DEST_EMAIL"
  run sudo sed -i '/root:/d' /etc/aliases
  echo "root: $DEST_EMAIL" | sudo_append_file /etc/aliases
  run sudo newaliases

  echo "  Enabling email for unattended-upgrades"
  sudo_write_file /etc/apt/apt.conf.d/91periph_email << EOF
    # Generated by https://github.com/periph/bootstrap
    Unattended-Upgrade::Mail "root@localhost";
EOF
  run sudo systemctl restart postfix

  # In the case of both USB network + wifi connectivity, which is possible on
  # RPi Zero and C.H.I.P., the two IP addresses will be listed on one line
  # separated with one space.
  local -r LOCAL_IP=$(hostname --all-ip-addresses)
  echo "  Sending a test email"
  cat <<EOF | run /usr/sbin/sendmail -t
FROM: setup.sh
TO: root@localhost
Subject: $HOST is configured!

This confirms sendmail works.

IP: $LOCAL_IP

`date`
EOF
  echo "  Check $DEST_EMAIL for an email from $HOST"
  echo "  Don't forget to configure the SPF record; it should look like:"
  echo "  v=spf1 include:_spf.google.com a:$HOST ~all"
  echo "  tail -f /var/log/mail.log"
}


function do_rename_host {
  echo "- do_rename_host: Renames the host"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  echo "  New hostname is: $HOST"
  if [ "$DIST" = "raspbian" ]; then
    run sudo raspi-config nonint do_hostname $HOST
  else
    #OLD="$(hostname)"
    #sudo sed -i "s/\$OLD/\$HOST/" /etc/hostname
    #sudo sed -i "s/\$OLD/\$HOST/" /etc/hosts
    # It hangs on the CHIP (?)
    run sudo hostnamectl set-hostname $HOST
  fi
}


function do_update_motd {
  echo "- do_update_motd: Updates motd"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  echo "Welcome to $HOST" | sudo_write_file /etc/motd
  if [ -f /etc/update-motd.d/10-help-text ]; then
    # This is just noise.
    run sudo chmod -x /etc/update-motd.d/10-help-text
  fi
}


function do_wifi {
  echo "- do_wifi: Configures wifi"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # First set the Country. This is important since it affects which wifi bands
  # and power limits can be used.
  if [ "$WIFI_COUNTRY" = "" ]; then
    echo "- Guessing country (will only work if wired network)"
    # Use http instead of https because the clock may not yet be set properly.
    WIFI_COUNTRY="$(curl -fsSL http://ipinfo.io/country 2>/dev/null || true)"
    echo "  Guessed country as \"$WIFI_COUNTRY\""
  fi
  if [ "$WIFI_COUNTRY" != "" ]; then
    if [ "$BOARD" = "raspberrypi" ]; then
      # RaspiOS.
      run sudo raspi-config nonint do_wifi_country $WIFI_COUNTRY
    elif [ -f /etc/default/crda ]; then
      # C.H.I.P.
      run sudo sed -i "s/REGDOMAIN=/REGDOMAIN=${WIFI_COUNTRY}/" /etc/default/crda
    else
      echo "  Do not know how to change wifi country to ${WIFI_COUNTRY}!"
    fi
  fi

  if (which connmanctl > /dev/null); then
    # connmanctl is used to configure wifi on the Beaglebone.
    #services=$(connmanctl services)
    #echo $services
    #line=$(echo $services | grep --fixed-strings "${WIFI_SSID}")
    #wifi_hash=$(echo $line | tr -s " " " " | cut -f 2 -d " ")
    sudo_write_file /var/lib/connman/wifi.config <<EOF
      # Generated by https://github.com/periph/bootstrap
      [General]
      PreferredTechnologies = ethernet,wifi,cellular
      [service_configured_wifi]
      Type = wifi
      Name = ${WIFI_SSID}
      Passphrase = ${WIFI_PASS}
      Favorite=true
      AutoConnect=true
EOF
    run sudo connmanctl connect configured_wifi
  elif (which nmcli > /dev/null); then
    # nmcli is used to configure wifi on the C.H.I.P.
    run sudo nmcli device wifi connect "$WIFI_SSID" password "$WIFI_PASS" ifname wlan0
  elif [ -f /etc/wpa_supplicant/wpa_supplicant.conf ]; then
    # wpa_supplicant file is used to configure wifi on RaspiOS.
    #wpa_passphrase MYSSID passphrase
    sudo_append_file /etc/wpa_supplicant/wpa_supplicant.conf <<EOF
      # Generated by https://github.com/periph/bootstrap
      network={
        ssid="${WIFI_SSID}"
        psk="${WIFI_PASS}"
        key_mgmt=WPA-PSK
      }
EOF
  else
    echo "  Do not know how to setup wifi to connect to ${WIFI_SSID}!"
    exit 1
  fi
}


function do_wifi_power {
  echo "- do_wifi_power: Disables wifi powersaving"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  if [ -f /etc/NetworkManager/conf.d/default-wifi-powersave-on.conf ]; then
    run sudo sed -i 's/wifi.powersave\w+=.+/wifi.powersave=2/' /etc/NetworkManager/conf.d/default-wifi-powersave-on.conf
    run sudo systemctl restart NetworkManager
  elif [ -f /sbin/iw ]; then
    # Wifi powersaving affects BeagleBone and C.H.I.P. quite badly.
    # For the BeagleBone, it's not too bad if SoftAp0 is kept on, as this forces
    # the wifi to continuously be awake. But once SoftAp0 is turned off, the
    # device is mostly unreachable.
    sudo_write_file /etc/udev/rules.d/70-disable-wifi-power-saving.rules <<EOF
ACTION=="add", SUBSYSTEM=="net", KERNEL=="wlan*", RUN+="/sbin/iw dev %k set power_save off"
EOF
    run sudo udevadm control --reload-rules
    run sudo udevadm trigger
  fi

  # On the BeagleBone, the above is *not* sufficient. We also must tweak the
  # driver directly.
  if [ -f /sys/kernel/debug/ieee80211/phy0/wlcore/sleep_auth ]; then
    sudo_write_file /etc/systemd/system/disable_wifi_power_saving.service <<EOF
      # Generated by https://github.com/periph/bootstrap
      [Unit]
      Description=Disable Wifi power saving
      After=network-online.service
      [Service]
      Type=oneshot
      Restart=no
      ExecStart=/bin/bash -c 'echo 0 > /sys/kernel/debug/ieee80211/phy0/wlcore/sleep_auth'
      [Install]
      WantedBy=default.target
EOF
    run sudo systemctl daemon-reload
    run sudo systemctl enable disable_wifi_power_saving
  fi
}


function do_swap {
  echo "- do_swap: Installs a 512MiB swap file at /var/swap"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  run sudo dd if=/dev/zero of=/var/swap bs=1K count=512K
  run sudo chmod 0600 /var/swap
  run sudo mkswap /var/swap
  run sudo swapon /var/swap
  sudo_append_file /etc/fstab  <<EOF
    /var/swap none swap sw 0 0
EOF
  # TODO(maruel): Configure to use as little swap as possible.
}


function do_sudo {
  echo "- do_sudo: Makes the default user a passwordless sudoer"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # This file is already present on RaspiOS.
  if [ ! -f /etc/sudoers.d/010_$USERNAME-nopasswd ]; then
    sudo_write_file /etc/sudoers.d/010_$USERNAME-nopasswd <<EOF
      $USERNAME ALL=(ALL) NOPASSWD: ALL
EOF
    chmod 0660 /etc/sudoers.d/010_$USERNAME-nopasswd
  fi
}


function do_self_update {
  echo "Replace the script with the latest version"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  if [ "$0" = bash ]; then
    echo ""
    echo "self_update is unsupported when curl'ed."
    exit 1
  fi

  if (which curl > /dev/null); then
    curl -sSL https://raw.githubusercontent.com/periph/bootstrap/master/setup.sh > $0
    chmod +x $0
    echo "Done!"
  else
    echo "Failed to find curl"
    exit 1
  fi
}


function do_all {
  echo "- do_all: Runs all default installation steps"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  detect_board
  detect_user

  # TODO(maruel): Add new commands:
  # - enable_uart on RaspiOS
  if [ "$WIFI_SSID" != "" ]; then
    do_wifi
  fi
  do_wifi_power
  wait_network
  do_apt
  if [ "$BOARD" = "beaglebone" ]; then
    do_beaglebone
  elif [ "$BOARD" = "chip" ]; then
    do_chip
  elif [ "$BOARD" = "odroid" ]; then
    do_odroid
  #elif [ "$BOARD" = "raspberrypi" ]; then
  elif [ "$DIST" = "raspbian" ]; then
    do_raspios
  fi
  do_ssh
  if [ $ACTION_GO -eq 1 ]; then
    # TODO(maruel): Do not run on C.H.I.P. Pro because of lack of space.
    do_golang
  fi
  do_unattended_upgrade
  do_rename_host
  if [ "$DEST_EMAIL" != "" ]; then
    do_sendmail
  fi
  do_timezone
  #do_sudo
  #do_swap
  do_update_motd
}


## Utility functions.


function run {
  if [ $DRY_RUN -eq 0 ]; then
    "$@"
  else
    echo "    Dry run: $*"
  fi
}


function sudo_write_file {
  awk 'NR == 1 {match($0, /^ */); l = RLENGTH + 1}{print substr($0, l)}' | run sudo tee $1 > /dev/null
  #sed 's/^ \+//g' | run sudo tee $1 > /dev/null
}


function sudo_append_file {
  # Always append an empty line before and after.
  echo '' | run sudo tee --append $1 > /dev/null
  awk 'NR == 1 {match($0, /^ */); l = RLENGTH + 1}{print substr($0, l)}' | run sudo tee --append $1 > /dev/null
  echo '' | run sudo tee --append $1 > /dev/null
  #sed 's/^ \+//g' | run sudo tee --append $1 > /dev/null
}


function wait_network {
  echo "- wait_network: Waiting for network to be up and running"
  until ping -c1 www.google.com &>/dev/null; do
    sleep 1
    #list_wifi
    #echo "  (trying again)"
  done
  echo "- Network is UP"
}


function list_wifi {
  # List wifi networks.
  if (which connmanctl > /dev/null); then
    run connmanctl scan wifi
  elif (which nmcli > /dev/null); then
    run nmcli device wifi list
  else
    iwlist wlan0 scan
    # iwconfig
  fi
}


function detect_board {
  # Defines variables BOARD, DIST, HOST and SERIAL.
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

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
  if [ "$DIST" = "raspbian" ]; then
    BOARD=raspberrypi
  fi
  if grep -q 'Raspberry Pi' /proc/cpuinfo; then
    BOARD=raspberrypi
  fi
  echo "  Detected board: $BOARD"

  # Generate a hostname based on the serial number of the CPU with leading zeros
  # trimmed off, it is a constant yet unique value.
  # Get the CPU serial number, otherwise the systemd machine ID.
  SERIAL="$(cat /proc/cpuinfo | grep Serial | cut -d ':' -f 2 | sed 's/^[ 0]\+//')"
  if [ "$SERIAL" = "" ]; then
    # Many ARM CPUs do not have a serial number. In this case, use the eMMC CID
    # register if available.
    # https://forum.odroid.com/viewtopic.php?f=80&t=3064
    export SERIAL="$(cat /sys/block/mmcblk?/device/cid 2>/dev/null | cut -c 25-)"
  fi
  if [ "$SERIAL" = "" ]; then
    # Fallback on systemd ID. It's not a good source in practice.
    SERIAL="$(hostnamectl status | grep 'Machine ID' | cut -d ':' -f 2 | cut -c 2-)"
  fi

  # Cut to keep the last 4 characters. Otherwise this quickly becomes unwieldy.
  # The first characters cannot be used because they matches when buying
  # multiple devices at once. 4 characters of hex encoded digits gives 65535
  # combinations. Taking in account there will be at most 255 devices on the
  # network subnet, it should be "good enough". Increase to 5 if needed.
  SERIAL="$(echo $SERIAL | sed 's/.*\(....\)/\1/')"

  # Intentionally use HOST to not clash with bash's HOSTNAME.
  HOST="$BOARD-$SERIAL"
}


function detect_user {
  # Assumes there is only one account. This is true for most distros. The value
  # is generally one of: pi, debian, odroid, chip.
  # TODO(maruel): This is brittle!
  USERNAME="$(ls /home)"
}


function conditional_reboot {
  if [ $ACTION_REBOOT -eq 1 ]; then
    sudo shutdown -r now
  fi
}


function show_help {
  if [ "$0" = bash ]; then
    echo ""
    echo "help is unsupported when curl'ed, please download first."
    exit 1
  fi

  cat << EOF
Usage: setup.sh [args] [command] [-- additional script before reboot]

Options:
  -h  --help             Prints this help page
  -d  --dry-run          Enable dry run mode; no system change occurs. Implies
                         -nr

  -5  --5inch            Enables 5" HDMI 800x480 display support (RaspiOS)
  -e  --email XXX        Email address to forward all root@localhost to
  -nr --no-reboot        Disable rebooting at the end
  -ng --no-go            Disable installing Go toolchain
  -sk --ssh-key FILE     SSH authorized_keys to copy to the home user directory
  -t  --timezone XXX     Timezone to use; default: $TIMEZONE
  -wc --wifi-country XXX Country for Wifi settings; if unset, try to guess it
                         but requires ethernet/USB network first
  -ws --wifi-ssid SSID   SSID to connect to
  -wp --wifi-pass PWD    Password to use for Wifi

Commands:
EOF

  for i in $(grep "^function do_" "$0" | cut -f 2 -d ' '); do
    echo -n "  "
    local LINE=$(bash "$0" --banner-only $i | cut -f 2- -d ':')
    printf "%-21s %s\\n" "$i" "$LINE"
  done

  cat << EOF

By default 'do_all' is run and the host is rebooted afterward. In this case, a
command line can be supplied after '--', it'll be run before rebooting the
host.
EOF
}


## Main.


# Default actions.
ACTION_5INCH=0
ACTION_GO=1
ACTION_SPI1=0   # TODO(maruel): Surface, may have side effect with UART and BT.
ACTION_REBOOT=1
BANNER_ONLY=0
DRY_RUN=0
DEST_EMAIL=""
SSH_KEY=""
# Use "timedatectl list-timezones" to list the values.
TIMEZONE="Etc/UTC"
# Must be an ISO/IEC 3166-1 alpha2 country code.
WIFI_COUNTRY=""
WIFI_SSID=""
WIFI_PASS=""


while [ $# -gt 0 ]; do
  # Consume the argument and switch on it.
  arg="$1"
  shift
  case "$arg" in
  # Arguments
  "--banner-only")  # Undocumented
    BANNER_ONLY=1
    ;;
  "-5" | "--5inch")
    ACTION_5INCH=1
    ;;
  "-d" | "--dry-run")
    echo "-> Dry run mode"
    DRY_RUN=1
    ACTION_REBOOT=0
    ;;
  "-e" | "--email")
    DEST_EMAIL=$1
    # TODO(maruel): Verify '@' is in the address, it doesn't start with '-', is
    # not empty.
    shift
    ;;
  "-h" | "--help" | "help")
    show_help
    exit 1
    ;;
  "-nr" | "--no-reboot")
    echo "-> No reboot"
    ACTION_REBOOT=0
    ;;
  "-ng" | "--no-go")
    echo "-> Skip installing Go"
    ACTION_GO=0
    ;;
  "-sk" | "--ssh-key")
    SSH_KEY=$1
    if [ ! -f $SSH_KEY ]; then
      echo "Error: $SSH_KEY is not a file"
    fi
    shift
    ;;
  "-t" | "--timezone")
    TIMEZONE=$1
    # TODO(maruel): Verify is not empty.
    shift
    ;;
  "-wc" | "--wifi-country")
    WIFI_COUNTRY=$1
    # TODO(maruel): Verify is not empty.
    shift
    ;;
  "-ws" | "--wifi-ssid")
    WIFI_SSID=$1
    # TODO(maruel): Verify is not empty.
    shift
    ;;
  "-wp" | "--wifi-pass")
    WIFI_PASS=$1
    # TODO(maruel): Verify is not empty.
    shift
    ;;

  # Commands
  do_*)
    detect_board
    detect_user
    $arg "$@"
    exit 0
    ;;

  "--")
    do_all
    if [ $# -gt 0 ]; then
      echo "-> Running custom command $*"
      run "$@"
    fi
    conditional_reboot
    exit 0
    ;;

  *)
    show_help
    echo ""
    echo "Unknown argument $arg"
    exit 2
    ;;
  esac
done


do_all
if [ $# -gt 0 ]; then
  echo "-> Running custom command $*"
  run "$@"
fi
conditional_reboot
