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

  # The Beaglebone comes with a lot of packages preinstalled, which fills up the
  # small 4Gb eMMC quickly. Make some space as we won't be using these.
  #
  # Use the following to hunt and kill:
  #   dpkg --get-selections | less
  run sudo apt-get remove -y \
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
  run sudo apt-get purge -y apache2 mysql-common x11-common

  echo "  Enabling SPI"
  #git clone https://github.com/beagleboard/bb.org-overlays
  run cd /opt/source/bb.org-overlays
  run ./dtc-overlay.sh
  run ./install.sh
  sudo_append_file /boot/uEnv.txt <<EOF
    # Change made by https://github.com/periph/bootstrap
    cape_enable=bone_capemgr.enable_partno=BB-SPIDEV0
EOF
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
  # like it is done on Raspbian.
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
  run sudo useradd odroid --password odroid -M --shell /bin/bash \
    -G adm,cdrom,dialout,dip,fax,floppy,plugdev,sudo,tape,video
  if [ $DRY_RUN -eq 0 ]; then
    echo odroid:odroid | sudo chpasswd
  else
    echo "Dry run: echo odroid:odroid | sudo chpasswd"
  fi

  # /etc/skel won't be copied automatically when the directory already existed,
  # so forcibly do it now.
  run sudo cp /etc/skel/.[!.]* /home/odroid
  run sudo chown odroid:odroid /home/odroid/.[!.]*
  # This file is created automatically and owned by root.
  run rm -rf /home/odroid/resize.log
}


function do_raspberrypi {
  echo "- do_raspberrypi: Raspbian specific changes"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  run sudo apt-get -y remove triggerhappy
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

  if [ $KEEP_HDMI -eq 0 ]; then
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
  fi

  if [ $ACTION_5INCH -eq 1 ]; then
    sudo_append_file /boot/config.txt << EOF
      # Change made by https://github.com/periph/bootstrap
      # Enable support for 800x480 display:
      hdmi_group=2
      hdmi_mode=87
      hdmi_cvt 800 480 60 6 0 0 0
      # Enable touchscreen:
      # Not necessary on Jessie Lite since it boots in console mode. :)
      # Some displays use 22, others 25.
      # Enabling this means the SPI bus cannot be used anymore.
      #dtoverlay=ads7846,penirq=22,penirq_pull=2,speed=10000,xohms=150
EOF
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
}


## Generic changes.


function do_apt {
  echo "- do_apt: Run apt-get update & upgrade and install few apps"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  # Try to work around:
  #  WARNING: The following packages cannot be authenticated!
  # TODO(maruel): Likely not needed anymore.
  run sudo apt-key update

  run sudo DEBIAN_FRONTEND=noninteractive apt-get update
  run sudo DEBIAN_FRONTEND=noninteractive apt-get -qy upgrade
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
  run sudo DEBIAN_FRONTEND=noninteractive apt-get -qy install curl git ssh tmux unattended-upgrades vim
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

  # Assumes there is only one account. This is true for most distros. The value
  # is generally one of: pi, debian, odroid, chip.
  # TODO(maruel): This is brittle!
  # TODO(maruel): Inconditionally disable root access.
  # TODO(maruel): Enable ssh key as argument.
  USERNAME="$(ls /home)"

  if [ "${USER:=root}" != "root" ]; then
    mkdir -p .ssh
    # Do not cp to not copy the file attributes.
    cat $SSH_KEY >>.ssh/authorized_keys
  else
    mkdir -p /home/$USERNAME/.ssh
    cat $SSH_KEY >>/home/$USERNAME/.ssh/authorized_keys
    chown -R $USERNAME:$USERNAME /home/$USERNAME/.ssh
  fi

  if [ -f /home/$USERNAME/.ssh/authorized_keys ]; then
    echo "  Disabling ssh password authentication support"
    run sudo sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config
  fi

  # Some distros (like O-DROID with Ubuntu minimal) enable ssh as root. This is
  # not a good idea.
  echo "  Disable root ssh support"
  run sudo sed -i 's/PermitRootLogin yes/PermitRootLogin no/' /etc/ssh/sshd_config
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

  local GO_ARCH=$(dpkg --print-architecture)
  if [ "$GO_ARCH" = "armhf" ]; then
    GO_ARCH=armv6l
  fi
  local GO_OS_NAME=linux

  if [ "$(getconf LONG_BIT)" = "64" ]; then
    if [ "$GO_ARCH" = "arm" ]; then
      echo "  Falling back to local go toolchain compilation"
      do_golang_compile
      return 0
    fi
  fi

  # Magically figure out latest version for precompiled binaries.
  echo "  GO_ARCH=${GO_ARCH}  GO_OS_NAME=${GO_OS_NAME}"
  URL=`curl -sS https://golang.org/dl/ | grep -Po "https://.+\.com/.+/go[0-9.]+${GO_OS_NAME}-${GO_ARCH}.tar.gz" | head -n 1`
  FILENAME=`echo ${URL} | cut -d / -f 5`

  # TODO(maruel): If current == new, skip. This permits running this script
  # nightly.
  # local CURRENT=$(go version | cut -f 3 -d ' ' | cut -c 3-)

  # The non-guesswork version:
  #BASE_URL=https://redirector.gvt1.com/edgedl/go/
  #GO_VERSION=1.9.2
  #FILENAME=go${GO_VERSION}.${GO_OS_NAME}-${GO_ARCH}.tar.gz
  #URL=${BASE_URL}/${FILENAME}
  echo "  Fetching $URL"
  echo "    as $FILENAME"
  run curl -L -o $FILENAME -sS $URL
  if [ -d /usr/local/go ]; then
    echo "  Removing previous version in /usr/local/go"
    run sudo rm -rf /usr/local/go
  fi
  echo "  Extracting to /usr/local/go"
  run sudo tar -C /usr/local -xzf $FILENAME
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
    # Limit download speed to NN Kbps
    Acquire::http::Dl-Limit "100";
    # apt-get autoclean every NN days.
    APT::Periodic::AutocleanInterval "21";
EOF
  if [ ! -f /etc/apt/apt.conf.d/20auto-upgrades ]; then
    # The equivalent of: sudo dpkg-reconfigure unattended-upgrades
    # odroid has it by default, but raspbian doesn't.
    run sudo cp /usr/share/unattended-upgrades/20auto-upgrades /etc/apt/apt.conf.d/20auto-upgrades
  fi
}


function do_sendmail {
  echo "- do_sendmail: Enables sending emails + sends email upon apt-get actions"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  if [ "$DEST_EMAIL" = "" ]; then
    echo "  Must specify the email address to forward root@localhost to"
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

  echo "  Sending a test email"
  cat <<EOF | run /usr/sbin/sendmail -t
FROM: setup.sh
TO: root@localhost
Subject: $HOST is configured!

This confirms sendmail works.
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
  if [ "$BOARD" = "raspberrypi" ]; then
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
  echo "- do_wifi: Configures wifi and disables powersaving if relevant"
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
      # Raspbian.
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
    sudo_write_file /etc/modprobe.d/8192cu_disable_wifi_power_saving.conf << EOF
      # Generated by https://github.com/periph/bootstrap
      # Disable powersaving, otherwise the device is unusable as a server.
      # - rtw_power_mgnt=0 disables power saving
      # - rtw_enusbss=0 disables USB autosuspend
      options 8192cu rtw_power_mgnt=0 rtw_enusbss=0
EOF
    if [ -f /etc/wifidog.conf ]; then
      # Disable the automatic SoftAP0 on the BeagleBone.
      # /usr/bin/wifidog_pre, /etc/wifidog.conf, /etc/default/wifidog-gateway.
      run systemctl stop    wificonfig
      run systemctl disable wificonfig
      run systemctl stop    wifidog-gateway
      run systemctl disable wifidog-gateway
      run systemctl disable wifi-reset
    fi
  elif (which nmcli > /dev/null); then
    # nmcli is used to configure wifi on the C.H.I.P.
    run sudo nmcli device wifi connect "$WIFI_SSID" password "$WIFI_PASS" ifname wlan0
    # Disable powersaving, otherwise the device is unusable as a server.
    sudo_write_file /etc/systemd/system/disable_wifi_power_saving.service <<EOF
      # Generated by https://github.com/periph/bootstrap
      [Unit]
      Description=Disable Wifi power saving
      After=auditd.service
      [Service]
      Type=oneshot
      Restart=no
      ExecStart=iw wlan0 set power_save off
      [Install]
      WantedBy=default.target
EOF
    run sudo systemctl daemon-reload
    run sudo systemctl enable disable_wifi_power_saving
  elif [ -f /etc/wpa_supplicant/wpa_supplicant.conf ]; then
    # wpa_supplicant file is used to configure wifi on Raspbian.
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


function do_all {
  echo "- do_all: Runs all default installation steps"
  if [ $BANNER_ONLY -eq 1 ]; then return 0; fi

  detect_board

  # TODO(maruel): Add new commands:
  # - do_5inch
  # - enable_uart on Raspbian
  if [ "$WIFI_SSID" != "" ]; then
    do_wifi
  fi
  wait_network
  do_apt
  if [ "$BOARD" = "beaglebone" ]; then
    do_beaglebone
  elif [ "$BOARD" = "chip" ]; then
    do_chip
  elif [ "$BOARD" = "odroid" ]; then
    do_odroid
  elif [ "$BOARD" = "raspberrypi" ]; then
    do_raspberrypi
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
  echo "  Detected board: $BOARD"

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

  # Intentionally use HOST to not clash with bash's HOSTNAME.
  HOST="$BOARD-$SERIAL"
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

  -5  --5inch            Enables 5" HDMI 800x480 display support (Raspbian)
  -e  --email XXX        Email address to forward all root@localhost to
  -kh --keep-hdmi        Keeps HDMI enabled, default is to disable (Raspbian)
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
    LINE=$(bash "$0" --banner-only $i | cut -f 2- -d ':')
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
KEEP_HDMI=0
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
  "-kh" | "--keep-hdmi")
    echo "-> Keep HDMI enabled"
    KEEP_HDMI=1
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
