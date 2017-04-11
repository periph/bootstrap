#!/bin/sh
# Copyright 2017 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by a BSD-style license that can be found in the
# LICENSE file.

# Run as:
#   curl -sSL https://raw.githubusercontent.com/periph/bootstrap/master/rename_host.sh | bash
#   curl -sSL https://goo.gl/EkANh0 | bash

set -eu


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
  #OLD="$(hostname)"
  #sudo sed -i "s/\$OLD/\$HOST/" /etc/hostname
  #sudo sed -i "s/\$OLD/\$HOST/" /etc/hosts
  # It hangs on the CHIP (?)
  sudo hostnamectl set-hostname $HOST
fi


echo "- Changing MOTD"
echo "Welcome to $HOST" | sudo tee /etc/motd
