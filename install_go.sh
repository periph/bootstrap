#!/bin/sh
# Copyright 2017 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by a BSD-style license that can be found in the
# LICENSE file.

# Run as:
#   curl -sSL https://raw.githubusercontent.com/periph/bootstrap/master/install_go.sh | bash
#   curl -sSL https://goo.gl/TFmVMG | bash

set -eu


# Install the Go toolchain.
# TODO(maruel): Do not run on C.H.I.P. Pro because of lack of space.
# TODO(maruel): Magically figure out latest version.
GO_VERSION=1.8.3
# TODO(maruel): Detect if x86.
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
