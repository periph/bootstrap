#!/bin/sh
# Copyright 2017 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by a BSD-style license that can be found in the
# LICENSE file.

# Run as:
#   curl -sSL https://raw.githubusercontent.com/periph/bootstrap/master/install_go.sh | bash
#   curl -sSL https://goo.gl/TFmVMG | bash

set -eu


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
